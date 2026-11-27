package tencent

import (
	"bytes"
	"context"
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
	"unicode/utf8"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/capability"
	"github.com/NSObjects/go-speech/internal/errclass"
	"github.com/NSObjects/go-speech/internal/tc3"
	"github.com/NSObjects/go-speech/internal/ttscollect"
	"github.com/NSObjects/go-speech/internal/wireutil"
	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
)

const DefaultHTTPEndpoint = "https://tts.tencentcloudapi.com"

const maxHTTPResponse = 2 * ttscollect.MaxAudioBytes

// HTTPConfig configures native TextToVoice. AppID is not required by API 3.0.
type HTTPConfig struct {
	SecretID  string
	SecretKey string
	Token     string
	Region    string
	Endpoint  string
	Timeout   time.Duration
}

// HTTPOptions adds TextToVoice-only parameters to the native voice options.
type HTTPOptions struct {
	Options
	ProjectID       *int64
	ModelType       *int
	PrimaryLanguage *int
}

// HTTPClient implements native complete-text synthesis, not a streaming session.
type HTTPClient struct {
	cfg  HTTPConfig
	base *url.URL
	http *http.Client
}

var (
	_ tts.Synthesizer        = (*HTTPClient)(nil)
	_ tts.CapabilityProvider = (*HTTPClient)(nil)
)

func NewHTTP(cfg HTTPConfig) (*HTTPClient, error) {
	if strings.TrimSpace(cfg.SecretID) == "" || strings.TrimSpace(cfg.SecretKey) == "" ||
		strings.ContainsAny(cfg.SecretID, " ,/\r\n\t") || strings.ContainsAny(cfg.Region+cfg.Token, "\r\n") {
		return nil, invalid("invalid HTTP credentials or region")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultHTTPEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && wireutil.IsLoopback(u.Hostname()))) {
		return nil, invalid("HTTP endpoint must be an HTTPS origin")
	}
	if cfg.Timeout < 0 {
		return nil, invalid("negative HTTP timeout")
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

func SupportedHTTPAudio() audio.Capability {
	return audio.Capability{
		Encodings:   []audio.Encoding{audio.EncodingWAV, audio.EncodingPCM16LE, audio.EncodingMP3},
		SampleRates: []int{8000, 16000, 24000},
		Channels:    []int{1},
	}
}

func (c *HTTPClient) Capabilities() tts.Capabilities {
	if c == nil {
		return tts.Capabilities{}
	}
	f := (&Client{}).features()
	f[tts.FeatureLanguage] = capability.Feature{Status: capability.Supported, Parameters: []capability.Parameter{{Field: "Options.Provider.PrimaryLanguage", Kind: capability.Integer, IntValues: []int{1, 2}}}}
	f[tts.FeatureAlignment] = capability.Feature{Status: capability.Unknown, Notes: "Voice dependent; use HTTPClient.SynthesizeDetailed."}
	return tts.Capabilities{
		Provider: "tencent", Protocol: "TextToVoice", Audio: SupportedHTTPAudio(), Batch: true,
		CompleteTextFormats: []tts.TextFormat{tts.TextPlain, tts.TextSSML}, Features: f,
	}
}

type httpRequest struct {
	Text             string   `json:"Text"`
	SessionID        string   `json:"SessionId"`
	VoiceType        int64    `json:"VoiceType"`
	SampleRate       int      `json:"SampleRate"`
	Codec            string   `json:"Codec"`
	Speed            *float64 `json:"Speed,omitempty"`
	Volume           *float64 `json:"Volume,omitempty"`
	ProjectID        *int64   `json:"ProjectId,omitempty"`
	ModelType        *int     `json:"ModelType,omitempty"`
	PrimaryLanguage  *int     `json:"PrimaryLanguage,omitempty"`
	FastVoiceType    string   `json:"FastVoiceType,omitempty"`
	EnableSubtitle   *bool    `json:"EnableSubtitle,omitempty"`
	SegmentRate      *int     `json:"SegmentRate,omitempty"`
	EmotionCategory  string   `json:"EmotionCategory,omitempty"`
	EmotionIntensity *int     `json:"EmotionIntensity,omitempty"`
	EnableLatex      *bool    `json:"EnableLatex,omitempty"`
}

func prepareHTTP(req tts.Request, detailed bool) (httpRequest, audio.Format, error) {
	var p httpRequest
	f := req.Format
	if !utf8.ValidString(req.Text) || strings.TrimSpace(req.Text) == "" {
		return p, f, invalid("Text must be nonempty valid UTF-8")
	}
	switch req.TextFormat {
	case "", tts.TextPlain, tts.TextSSML:
	default:
		return p, f, &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "tencent", Operation: "TextToVoice", Err: tts.ErrUnsupportedTextFormat}
	}
	// The provider documents different Chinese and English limits but no
	// mixed-text or SSML counting algorithm. Do not guess from UTF-8 bytes.
	// For plain ASCII the English limit is unambiguous; otherwise delegate
	// language-specific counting to the provider.
	if req.TextFormat != tts.TextSSML && len(req.Text) == utf8.RuneCountInString(req.Text) && len(req.Text) > 500 {
		return p, f, invalid("plain ASCII text exceeds 500 characters")
	}
	if len(req.Text) > 64<<10 {
		return p, f, invalid("Text exceeds SDK request limit")
	}
	id, err := strconv.ParseInt(req.Voice, 10, 64)
	if err != nil || id < 0 {
		return p, f, invalid("Voice must be a numeric Tencent VoiceType")
	}
	if f == (audio.Format{}) {
		f = audio.Format{Encoding: audio.EncodingWAV, SampleRate: 16000, Channels: 1}
	}
	if err := f.Validate(); err != nil {
		return p, f, invalid(err.Error())
	}
	if err := SupportedHTTPAudio().Validate(f); err != nil {
		return p, f, &speecherr.Error{Code: speecherr.CodeUnsupported, Provider: "tencent", Operation: "TextToVoice", Err: err}
	}
	var opt HTTPOptions
	switch x := req.Options.Provider.(type) {
	case nil:
	case Options:
		opt.Options = x
	case *Options:
		if x == nil {
			return p, f, invalid("nil provider options")
		}
		opt.Options = *x
	case HTTPOptions:
		opt = x
	case *HTTPOptions:
		if x == nil {
			return p, f, invalid("nil HTTP options")
		}
		opt = *x
	default:
		return p, f, invalid("provider options do not belong to tencent")
	}
	req.Options.Provider = opt.Options
	req.Voice = strconv.FormatInt(id, 10)
	if _, err := requestVoiceQuery(req); err != nil {
		return p, f, err
	}
	if opt.ProjectID != nil && *opt.ProjectID < 0 || opt.ModelType != nil && *opt.ModelType != 1 ||
		opt.PrimaryLanguage != nil && *opt.PrimaryLanguage != 1 && *opt.PrimaryLanguage != 2 {
		return p, f, invalid("invalid TextToVoice project, model or language")
	}
	if !detailed && opt.EnableSubtitle != nil && *opt.EnableSubtitle {
		return p, f, invalid("use HTTPClient.SynthesizeDetailed for subtitles")
	}
	codec := string(f.Encoding)
	if f.Encoding == audio.EncodingPCM16LE {
		codec = "pcm"
	}
	p = httpRequest{
		Text: req.Text, VoiceType: id, SampleRate: f.SampleRate, Codec: codec,
		Speed: opt.Speed, Volume: opt.Volume, ProjectID: opt.ProjectID, ModelType: opt.ModelType,
		PrimaryLanguage: opt.PrimaryLanguage, FastVoiceType: opt.FastVoiceType, EnableSubtitle: opt.EnableSubtitle,
		SegmentRate: opt.SegmentRate, EmotionCategory: opt.EmotionCategory, EmotionIntensity: opt.EmotionIntensity,
		EnableLatex: opt.EnableLatex,
	}
	return p, f, nil
}

// HTTPSubtitle preserves nullable native phonemes and indices.
type HTTPSubtitle struct {
	Text       string  `json:"Text"`
	BeginTime  *int64  `json:"BeginTime"`
	EndTime    *int64  `json:"EndTime"`
	BeginIndex *int    `json:"BeginIndex"`
	EndIndex   *int    `json:"EndIndex"`
	Phoneme    *string `json:"Phoneme"`
}

// HTTPResult includes the complete audio, native subtitles and diagnostic IDs.
type HTTPResult struct {
	tts.Result
	RequestID string
	SessionID string
	Subtitles []HTTPSubtitle
}

func (c *HTTPClient) ValidateRequest(req tts.Request) error {
	_, _, err := prepareHTTP(req, true)
	return err
}

func (c *HTTPClient) ValidateSynthesize(req tts.Request) error {
	_, _, err := prepareHTTP(req, false)
	return err
}

func (c *HTTPClient) Synthesize(ctx context.Context, req tts.Request) (audio.Buffer, error) {
	r, err := c.synthesize(ctx, req, false)
	return r.Audio, err
}

func (c *HTTPClient) SynthesizeDetailed(ctx context.Context, req tts.Request) (HTTPResult, error) {
	return c.synthesize(ctx, req, true)
}

func (c *HTTPClient) synthesize(ctx context.Context, req tts.Request, detailed bool) (HTTPResult, error) {
	if err := ctx.Err(); err != nil {
		return HTTPResult{}, err
	}
	if c == nil || c.base == nil || c.http == nil {
		return HTTPResult{}, invalid("uninitialized HTTP client")
	}
	p, f, err := prepareHTTP(req, detailed)
	if err != nil {
		return HTTPResult{}, err
	}
	p.SessionID, err = wireutil.UUIDv4()
	if err != nil {
		return HTTPResult{}, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return HTTPResult{}, invalid("invalid HTTP request")
	}
	h, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String(), bytes.NewReader(body))
	if err != nil {
		return HTTPResult{}, err
	}
	h.GetBody = nil
	now := time.Now()
	h.Header.Set("Content-Type", tc3.ContentType)
	h.Header.Set("X-TC-Action", "TextToVoice")
	h.Header.Set("X-TC-Version", "2019-08-23")
	h.Header.Set("X-TC-Timestamp", strconv.FormatInt(now.Unix(), 10))
	h.Header.Set("Authorization", tc3.Authorization(c.cfg.SecretID, c.cfg.SecretKey, h.URL.Host, "TextToVoice", "tts", now, body))
	if c.cfg.Region != "" {
		h.Header.Set("X-TC-Region", c.cfg.Region)
	}
	if c.cfg.Token != "" {
		h.Header.Set("X-TC-Token", c.cfg.Token)
	}
	resp, err := c.http.Do(h)
	if err != nil {
		return HTTPResult{}, httpTransportError(ctx, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponse+1))
	if err != nil {
		return HTTPResult{}, httpTransportError(ctx, err)
	}
	if len(raw) > maxHTTPResponse {
		return HTTPResult{}, protocol("HTTP synthesis response exceeds limit")
	}
	if err := ctx.Err(); err != nil {
		return HTTPResult{}, err
	}
	return decodeHTTPResult(raw, resp.StatusCode, p.SessionID, f)
}

func httpTransportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	code, _ := errclass.Dial(err, nil)
	return &speecherr.Error{Code: code, Provider: "tencent", Operation: "TextToVoice", Err: errors.New("HTTP synthesis transport failed")}
}

func decodeHTTPResult(raw []byte, status int, session string, f audio.Format) (HTTPResult, error) {
	var env struct {
		Response *struct {
			Audio     []byte
			SessionID string         `json:"SessionId"`
			RequestID string         `json:"RequestId"`
			Subtitles []HTTPSubtitle
			Error *struct {
				Code    string
				Message string
			}
		}
	}
	err := json.Unmarshal(raw, &env)
	if status != http.StatusOK || env.Response != nil && env.Response.Error != nil {
		code, _ := errclass.HTTPStatus(status)
		var remote, message, requestID string
		if env.Response != nil {
			requestID = env.Response.RequestID
			if env.Response.Error != nil {
				remote, message = env.Response.Error.Code, env.Response.Error.Message
			}
		}
		switch {
		case strings.HasPrefix(remote, "AuthFailure"), strings.HasPrefix(remote, "UnauthorizedOperation"),
			remote == "UnsupportedOperation.AuthorizationExpired", remote == "UnsupportedOperation.AuthorizationFailed",
			remote == "UnsupportedOperation.ForbiddenUse", remote == "UnsupportedOperation.ServerNotOpen",
			remote == "InvalidParameterValue.AppIdNotRegistered":
			code = speecherr.CodeUnauthorized
		case strings.HasPrefix(remote, "RequestLimitExceeded"), strings.HasPrefix(remote, "LimitExceeded"),
			remote == "InternalError.ExceedMaxLimit", remote == "UnsupportedOperation.AccountArrears",
			remote == "UnsupportedOperation.NoFreeAccount", remote == "UnsupportedOperation.PkgExhausted":
			code = speecherr.CodeRateLimited
		case strings.HasPrefix(remote, "InvalidParameter"), strings.HasPrefix(remote, "MissingParameter"),
			remote == "UnsupportedOperation.TextTooLong":
			code = speecherr.CodeInvalidArgument
		case remote == "InternalError.NoResource", remote == "InternalError.ErrorGetRoute",
			remote == "UnsupportedOperation.ServerStopped", remote == "UnsupportedOperation.ServerDestoryed":
			code = speecherr.CodeUnavailable
		case remote == "UnsupportedOperation":
			code = speecherr.CodeUnsupported
		case status == http.StatusOK:
			code = speecherr.CodeInternal
		}
		return HTTPResult{}, &speecherr.Error{Code: code, Provider: "tencent", Operation: "TextToVoice", RemoteCode: remote, Err: fmt.Errorf("HTTP %d request_id=%s: %s", status, requestID, message)}
	}
	r := env.Response
	if err != nil || r == nil || r.SessionID != session || r.RequestID == "" || len(r.Audio) == 0 || len(r.Audio) > ttscollect.MaxAudioBytes {
		return HTTPResult{}, protocol("invalid TextToVoice response")
	}
	if f.Encoding == audio.EncodingPCM16LE && len(r.Audio)%2 != 0 {
		return HTTPResult{}, protocol("unaligned PCM response")
	}
	if f.Encoding == audio.EncodingWAV {
		b, err := audio.DecodeWAVPCM16(r.Audio)
		if err != nil || len(b.Data) == 0 || b.Format.SampleRate != f.SampleRate || b.Format.Channels != f.Channels {
			return HTTPResult{}, protocol("invalid WAV response or format mismatch")
		}
	}
	if f.Encoding == audio.EncodingMP3 {
		if encoding, err := audio.Detect(r.Audio); err != nil || encoding != audio.EncodingMP3 {
			return HTTPResult{}, protocol("invalid MP3 response or format mismatch")
		}
	}
	if len(r.Subtitles) > 100000 {
		return HTTPResult{}, protocol("too many subtitles")
	}
	result := HTTPResult{RequestID: r.RequestID, SessionID: r.SessionID, Subtitles: r.Subtitles}
	result.Audio = audio.Buffer{Data: r.Audio, Format: f}
	if len(r.Subtitles) != 0 {
		var a tts.Alignment
		var details AlignmentDetails
		for _, w := range r.Subtitles {
			if w.BeginTime == nil || w.EndTime == nil || *w.BeginTime < 0 || *w.EndTime < *w.BeginTime ||
				*w.EndTime > math.MaxInt64/int64(time.Millisecond) {
				return HTTPResult{}, protocol("invalid subtitle timestamps")
			}
			word := tts.Word{Text: w.Text, StartTime: time.Duration(*w.BeginTime) * time.Millisecond, EndTime: time.Duration(*w.EndTime) * time.Millisecond}
			if w.Phoneme != nil {
				word.Phoneme = *w.Phoneme
			}
			a.Words = append(a.Words, word)
			details.Indices = append(details.Indices, TextIndex{Begin: w.BeginIndex, End: w.EndIndex})
		}
		a.Provider = details
		result.Alignments = []tts.Alignment{a}
	}
	return result, nil
}
