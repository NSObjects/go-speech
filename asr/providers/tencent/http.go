package tencent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/capability"
	"github.com/NSObjects/go-speech/internal/errclass"
	"github.com/NSObjects/go-speech/internal/tc3"
	"github.com/NSObjects/go-speech/internal/wireutil"
	"github.com/NSObjects/go-speech/speecherr"
)

const DefaultHTTPEndpoint = "https://asr.tencentcloudapi.com"

const maxSentenceEncodedBytes = 3000000

// HTTPConfig selects the native SentenceRecognition API. AppID is unnecessary.
type HTTPConfig struct {
	SecretID    string
	SecretKey   string
	Token       string
	Endpoint    string
	EngineModel string
	Timeout     time.Duration
}

// HTTPOptions contains only controls documented for SentenceRecognition.
// FilterPunc accepts 0, 1, 2; ConvertNumMode accepts 0, 1.
// Temporary Hotwords take precedence over HotwordID on the server.
type HTTPOptions struct {
	WordInfo        *int
	FilterDirty     *int
	FilterModal     *int
	FilterPunc      *int
	ConvertNumMode  *int
	HotwordID       string
	Hotwords        []Hotword
	CustomizationID string
	ReplaceTextID   string
	InputSampleRate *int
}

func (HTTPOptions) ASRProviderOptions() {}

// SentenceFormat is the native wire format, including formats not modeled
// by the common audio package. Native input is never transcoded.
type SentenceFormat string

const (
	SentenceWAV     SentenceFormat = "wav"
	SentencePCM     SentenceFormat = "pcm"
	SentenceOggOpus SentenceFormat = "ogg-opus"
	SentenceSpeex   SentenceFormat = "speex"
	SentenceSILK    SentenceFormat = "silk"
	SentenceMP3     SentenceFormat = "mp3"
	SentenceM4A     SentenceFormat = "m4a"
	SentenceAAC     SentenceFormat = "aac"
	SentenceAMR     SentenceFormat = "amr"
)

type HTTPClient struct {
	cfg  HTTPConfig
	base *url.URL
	http *http.Client
}

var (
	_ asr.Recognizer         = (*HTTPClient)(nil)
	_ asr.CapabilityProvider = (*HTTPClient)(nil)
)

func NewHTTP(cfg HTTPConfig) (*HTTPClient, error) {
	if strings.TrimSpace(cfg.SecretID) == "" || strings.TrimSpace(cfg.SecretKey) == "" ||
		strings.ContainsAny(cfg.SecretID, " ,/\r\n\t") || strings.ContainsAny(cfg.Token, "\r\n") {
		return nil, argumentError("invalid HTTP credentials")
	}
	if cfg.EngineModel == "" {
		cfg.EngineModel = "16k_zh"
	}
	switch cfg.EngineModel {
	case "8k_zh", "8k_en", "16k_zh", "16k_zh-PY", "16k_zh_medical", "16k_en", "16k_yue",
		"16k_ja", "16k_ko", "16k_vi", "16k_ms", "16k_id", "16k_fil", "16k_th", "16k_pt",
		"16k_tr", "16k_ar", "16k_es", "16k_hi", "16k_fr", "16k_de", "16k_zh_dialect":
	default:
		return nil, argumentError("unknown SentenceRecognition engine")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultHTTPEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && wireutil.IsLoopback(u.Hostname()))) {
		return nil, argumentError("HTTP endpoint must be an HTTPS origin")
	}
	if cfg.Timeout < 0 {
		return nil, argumentError("negative HTTP timeout")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 120 * time.Second
	}
	u.Path = "/"
	return &HTTPClient{cfg: cfg, base: u, http: &http.Client{
		Timeout: cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *HTTPClient) Capabilities() asr.Capabilities {
	if c == nil {
		return asr.Capabilities{}
	}
	return asr.Capabilities{
		Provider: "tencent", Model: c.cfg.EngineModel, Protocol: "SentenceRecognition", Batch: true,
		Audio: audio.Capability{Encodings: []audio.Encoding{
			audio.EncodingPCM16LE, audio.EncodingWAV, audio.EncodingMP3, audio.EncodingOpus,
			audio.EncodingAAC, audio.EncodingAMR,
		}, Channels: []int{1}},
		Features: capability.Features{
			asr.FeatureWordTimestamps: {Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.WordInfo", Kind: capability.Integer, IntValues: []int{0, 1, 2}}}},
			asr.FeatureHotwords: {Status: capability.Supported, Notes: "Temporary list takes precedence over persistent ID."},
			asr.FeatureEmotion: {Status: capability.Unsupported},
			asr.FeatureSpeakerDiarization: {Status: capability.Unsupported},
			asr.FeatureSegmentTimestamps: {Status: capability.Unsupported, Notes: "Only whole audio duration and word timestamps."},
		},
	}
}

type sentenceRequest struct {
	Engine          string         `json:"EngSerViceType"`
	SourceType      int            `json:"SourceType"`
	VoiceFormat     SentenceFormat `json:"VoiceFormat"`
	URL             string         `json:"Url,omitempty"`
	Data            []byte         `json:"Data,omitempty"`
	DataLen         int            `json:"DataLen,omitempty"`
	WordInfo        *int           `json:"WordInfo,omitempty"`
	FilterDirty     *int           `json:"FilterDirty,omitempty"`
	FilterModal     *int           `json:"FilterModal,omitempty"`
	FilterPunc      *int           `json:"FilterPunc,omitempty"`
	ConvertNumMode  *int           `json:"ConvertNumMode,omitempty"`
	HotwordID       string         `json:"HotwordId,omitempty"`
	HotwordList     string         `json:"HotwordList,omitempty"`
	CustomizationID string         `json:"CustomizationId,omitempty"`
	ReplaceTextID   string         `json:"ReplaceTextId,omitempty"`
	InputSampleRate *int           `json:"InputSampleRate,omitempty"`
}

func sentenceFormat(f audio.Encoding) (SentenceFormat, error) {
	switch f {
	case audio.EncodingPCM16LE:
		return SentencePCM, nil
	case audio.EncodingWAV:
		return SentenceWAV, nil
	case audio.EncodingOpus:
		return SentenceOggOpus, nil
	case audio.EncodingMP3:
		return SentenceMP3, nil
	case audio.EncodingAAC:
		return SentenceAAC, nil
	case audio.EncodingAMR:
		return SentenceAMR, nil
	default:
		return "", unsupportedError("use native RecognizeBytes for provider-specific formats")
	}
}

func (c *HTTPClient) prepareSentence(format SentenceFormat, cfg asr.Config) (sentenceRequest, error) {
	var p sentenceRequest
	if c == nil || c.base == nil || c.http == nil {
		return p, argumentError("uninitialized HTTP client")
	}
	switch format {
	case SentencePCM, SentenceWAV, SentenceOggOpus, SentenceSpeex, SentenceSILK, SentenceMP3, SentenceM4A, SentenceAAC, SentenceAMR:
	default:
		return p, unsupportedError("unknown SentenceRecognition format")
	}
	if cfg.Language != "" || cfg.Options.Punctuation != nil || cfg.Options.TextNormalization != nil {
		return p, unsupportedError("use EngineModel, FilterPunc and ConvertNumMode")
	}
	var o HTTPOptions
	switch v := cfg.Options.Provider.(type) {
	case nil:
	case HTTPOptions:
		o = v
	case *HTTPOptions:
		if v == nil {
			return p, argumentError("nil HTTP options")
		}
		o = *v
	default:
		return p, unsupportedError("use native HTTPOptions for SentenceRecognition")
	}
	if cfg.Options.DisfluencyRemoval != nil {
		if o.FilterModal != nil {
			return p, argumentError("DisfluencyRemoval conflicts with FilterModal")
		}
		mode := 0
		if *cfg.Options.DisfluencyRemoval {
			mode = 1
		}
		o.FilterModal = &mode
	}
	if cfg.Options.UtteranceDetails && o.WordInfo == nil {
		mode := 1
		o.WordInfo = &mode
	}
	for _, v := range []*int{o.WordInfo, o.FilterDirty, o.FilterModal, o.FilterPunc} {
		if v != nil && (*v < 0 || *v > 2) {
			return p, argumentError("word/filter mode must be 0, 1 or 2")
		}
	}
	if o.ConvertNumMode != nil && (*o.ConvertNumMode < 0 || *o.ConvertNumMode > 1) {
		return p, argumentError("ConvertNumMode must be 0 or 1")
	}
	if o.InputSampleRate != nil && (*o.InputSampleRate != 8000 || format != SentencePCM || !strings.HasPrefix(c.cfg.EngineModel, "16k_")) {
		return p, argumentError("InputSampleRate=8000 requires PCM and a 16k engine")
	}
	hotwords, err := weightedHints(c.cfg.EngineModel, cfg.Hints, o.Hotwords)
	if err != nil {
		return p, err
	}
	p = sentenceRequest{
		Engine: c.cfg.EngineModel, VoiceFormat: format, WordInfo: o.WordInfo,
		FilterDirty: o.FilterDirty, FilterModal: o.FilterModal, FilterPunc: o.FilterPunc,
		ConvertNumMode: o.ConvertNumMode, HotwordID: o.HotwordID, HotwordList: hotwords,
		CustomizationID: o.CustomizationID, ReplaceTextID: o.ReplaceTextID, InputSampleRate: o.InputSampleRate,
	}
	return p, nil
}

// Recognize implements the common whole-buffer API. It validates known
// PCM/WAV metadata; compressed audio duration is validated by Tencent.
func (c *HTTPClient) Recognize(ctx context.Context, input audio.Buffer, cfg asr.Config) (asr.Result, error) {
	if err := ctx.Err(); err != nil {
		return asr.Result{}, err
	}
	if len(input.Data) == 0 || len(input.Data) > maxSentenceEncodedBytes || base64.StdEncoding.EncodedLen(len(input.Data)) > maxSentenceEncodedBytes {
		return asr.Result{}, argumentError("audio must fit the 3 MB base64 limit")
	}
	if cfg.Format != (audio.Format{}) && cfg.Format != input.Format {
		return asr.Result{}, argumentError("input and configured formats disagree")
	}
	if err := input.Format.Validate(); err != nil {
		return asr.Result{}, argumentError(err.Error())
	}
	if input.Format.Channels != 1 {
		return asr.Result{}, unsupportedError("common SentenceRecognition input must be mono")
	}
	format, err := sentenceFormat(input.Format.Encoding)
	if err != nil {
		return asr.Result{}, err
	}
	p, err := c.prepareSentence(format, cfg)
	if err != nil {
		return asr.Result{}, err
	}
	if format == SentencePCM {
		rate := sentencePCMRate(p)
		if input.Format.SampleRate != rate {
			return asr.Result{}, argumentError("PCM sample rate does not match engine or InputSampleRate")
		}
	}
	if format == SentenceWAV {
		b, err := audio.DecodeWAVPCM16(input.Data)
		if err != nil || b.Format.SampleRate != input.Format.SampleRate || b.Format.Channels != input.Format.Channels {
			return asr.Result{}, argumentError("invalid WAV or mismatched metadata")
		}
		if int64(len(b.Data)) > 120*int64(b.Format.SampleRate) {
			return asr.Result{}, argumentError("WAV exceeds 60 seconds")
		}
	}
	return c.recognizeBytes(ctx, p, input.Data)
}

func sentencePCMRate(p sentenceRequest) int {
	if p.InputSampleRate != nil || strings.HasPrefix(p.Engine, "8k_") {
		return 8000
	}
	return 16000
}

// RecognizeBytes passes provider-native encoded bytes without inventing a
// common encoding for SILK, M4A or native Speex. cfg.Format must be unset.
func (c *HTTPClient) RecognizeBytes(ctx context.Context, data []byte, format SentenceFormat, cfg asr.Config) (asr.Result, error) {
	if cfg.Format != (audio.Format{}) {
		return asr.Result{}, argumentError("native byte input requires an unset common Format")
	}
	p, err := c.prepareSentence(format, cfg)
	if err != nil {
		return asr.Result{}, err
	}
	return c.recognizeBytes(ctx, p, data)
}

func (c *HTTPClient) recognizeBytes(ctx context.Context, p sentenceRequest, data []byte) (asr.Result, error) {
	if len(data) == 0 || len(data) > maxSentenceEncodedBytes || base64.StdEncoding.EncodedLen(len(data)) > maxSentenceEncodedBytes {
		return asr.Result{}, argumentError("audio must fit the 3 MB base64 limit")
	}
	if p.VoiceFormat == SentencePCM && (len(data)%2 != 0 || len(data) > 120*sentencePCMRate(p)) {
		return asr.Result{}, argumentError("invalid PCM frames or duration exceeds 60 seconds")
	}
	p.SourceType, p.Data, p.DataLen = 1, data, len(data)
	return c.callSentence(ctx, p)
}

// RecognizeURL submits the URL to Tencent. The SDK never downloads it.
// The caller must ensure the URL is accessible and the audio meets limits.
func (c *HTTPClient) RecognizeURL(ctx context.Context, source string, format SentenceFormat, cfg asr.Config) (asr.Result, error) {
	if cfg.Format != (audio.Format{}) {
		return asr.Result{}, argumentError("URL input requires an unset common Format")
	}
	u, err := url.Parse(source)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return asr.Result{}, argumentError("invalid audio URL")
	}
	p, err := c.prepareSentence(format, cfg)
	if err != nil {
		return asr.Result{}, err
	}
	p.URL = source
	return c.callSentence(ctx, p)
}

// SentenceDetails preserves native duration, request ID and nullable word count.
type SentenceDetails struct {
	RequestID     string
	AudioDuration time.Duration
	WordSize      *int
	WordList      []SentenceWord
}

type SentenceWord struct {
	Word      string `json:"Word"`
	StartTime *int64 `json:"StartTime"`
	EndTime   *int64 `json:"EndTime"`
}

func (c *HTTPClient) callSentence(ctx context.Context, p sentenceRequest) (asr.Result, error) {
	if err := ctx.Err(); err != nil {
		return asr.Result{}, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return asr.Result{}, argumentError("invalid sentence request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String(), bytes.NewReader(body))
	if err != nil {
		return asr.Result{}, argumentError("invalid sentence HTTP request")
	}
	req.GetBody = nil
	now := time.Now()
	req.Header.Set("Content-Type", tc3.ContentType)
	req.Header.Set("X-TC-Action", "SentenceRecognition")
	req.Header.Set("X-TC-Version", "2019-06-14")
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("Authorization", tc3.Authorization(c.cfg.SecretID, c.cfg.SecretKey, req.URL.Host, "SentenceRecognition", "asr", now, body))
	if c.cfg.Token != "" {
		req.Header.Set("X-TC-Token", c.cfg.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return asr.Result{}, sentenceTransportError(ctx, err)
	}
	defer resp.Body.Close()
	const limit = 8 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return asr.Result{}, sentenceTransportError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return asr.Result{}, err
	}
	if len(raw) > limit {
		return asr.Result{}, protocolError("sentence response exceeds limit")
	}
	return decodeSentence(raw, resp.StatusCode)
}

func sentenceTransportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	code, _ := errclass.Dial(err, nil)
	return &speecherr.Error{Provider: "tencent", Operation: "SentenceRecognition", Code: code, Err: errors.New("sentence HTTP transport failed")}
}

func decodeSentence(raw []byte, status int) (asr.Result, error) {
	var env struct {
		Response *struct {
			Result        *string
			AudioDuration *int64
			WordSize      *int
			WordList      []SentenceWord
			RequestID     string `json:"RequestId"`
			Error         *struct {
				Code    string
				Message string
			}
		}
	}
	err := json.Unmarshal(raw, &env)
	r := env.Response
	if status != http.StatusOK || r != nil && r.Error != nil {
		code, _ := errclass.HTTPStatus(status)
		var remote, trace string
		if r != nil {
			trace = r.RequestID
			if r.Error != nil {
				remote = r.Error.Code
			}
		}
		switch {
		case strings.HasPrefix(remote, "AuthFailure"), strings.HasPrefix(remote, "UnauthorizedOperation"), remote == "FailedOperation.UserNotRegistered":
			code = speecherr.CodeUnauthorized
		case strings.HasPrefix(remote, "RequestLimitExceeded"), strings.HasPrefix(remote, "LimitExceeded"),
			remote == "FailedOperation.ServiceIsolate", remote == "FailedOperation.UserHasNoAmount", remote == "FailedOperation.UserHasNoFreeAmount":
			code = speecherr.CodeRateLimited
		case strings.HasPrefix(remote, "InvalidParameter"), strings.HasPrefix(remote, "MissingParameter"):
			code = speecherr.CodeInvalidArgument
		case status == http.StatusOK:
			code = speecherr.CodeInternal
		}
		// Remote messages may contain private URL query strings or transcript.
		return asr.Result{}, &speecherr.Error{Provider: "tencent", Operation: "SentenceRecognition", Code: code, RemoteCode: remote, Err: fmt.Errorf("HTTP %d request_id=%s", status, trace)}
	}
	if err != nil || r == nil || r.Result == nil || r.AudioDuration == nil || *r.AudioDuration < 0 ||
		*r.AudioDuration > math.MaxInt64/int64(time.Millisecond) || r.RequestID == "" {
		return asr.Result{}, protocolError("invalid sentence response")
	}
	if r.WordSize != nil && (*r.WordSize < 0 || r.WordList != nil && *r.WordSize != len(r.WordList)) {
		return asr.Result{}, protocolError("invalid sentence word count")
	}
	d := SentenceDetails{RequestID: r.RequestID, AudioDuration: time.Duration(*r.AudioDuration) * time.Millisecond, WordSize: r.WordSize, WordList: r.WordList}
	out := asr.Result{Text: *r.Result, Provider: d}
	if r.WordList != nil {
		u := asr.Utterance{Text: *r.Result, Definite: true}
		for _, w := range r.WordList {
			if w.StartTime == nil || w.EndTime == nil || *w.StartTime < 0 || *w.EndTime < *w.StartTime ||
				*w.EndTime > math.MaxInt64/int64(time.Millisecond) {
				return asr.Result{}, protocolError("invalid sentence word timestamps")
			}
			start, end := time.Duration(*w.StartTime)*time.Millisecond, time.Duration(*w.EndTime)*time.Millisecond
			u.Words = append(u.Words, asr.Word{Text: w.Word, StartTime: &start, EndTime: &end})
		}
		out.Utterances = []asr.Utterance{u}
	}
	return out, nil
}
