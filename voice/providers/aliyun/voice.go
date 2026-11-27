package aliyun

import (
	"context"
	"encoding/base64"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/voice"
)

const maxSampleBytes = 10_000_000

// Metadata preserves the operation's request ID and native usage count.
type Metadata struct {
	RequestID string `json:"request_id"`
	Usage     *Usage `json:"usage"`
}

type Usage struct {
	Count *int `json:"count"`
}

// AudioSource selects a public HTTP(S) URL or local encoded audio bytes.
// The SDK does not fetch URLs or decode/transcode recordings.
type AudioSource struct {
	URL    string
	Data   []byte
	Format SampleFormat
}

type SampleFormat string

const (
	WAV SampleFormat = "wav"
	MP3 SampleFormat = "mp3"
	M4A SampleFormat = "m4a"
)

func (a AudioSource) wire() (string, error) {
	if (a.URL == "") == (len(a.Data) == 0) {
		return "", invalid("provide exactly one audio URL or sample")
	}
	if a.URL != "" {
		u, err := url.Parse(a.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || a.Format != "" {
			return "", invalid("invalid audio URL or redundant format")
		}
		return a.URL, nil
	}
	if len(a.Data) > maxSampleBytes {
		return "", invalid("sample exceeds 10 MB")
	}
	var mime string
	switch a.Format {
	case WAV:
		mime = "audio/wav"
	case MP3:
		mime = "audio/mpeg"
	case M4A:
		mime = "audio/mp4"
	default:
		return "", invalid("sample format must be WAV, MP3 or M4A")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(a.Data), nil
}

func validPrefix(s string) bool {
	if len(s) < 1 || len(s) > 10 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func validID(s string) bool {
	return s != "" && len(s) <= 512 && utf8.ValidString(s) &&
		strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\r\n\t ")
}

func validText(s string, min, max int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" &&
		utf8.RuneCountInString(s) >= min && utf8.RuneCountInString(s) <= max
}

func qwenModel(model string) bool {
	switch model {
	case "qwen-audio-3.0-tts-plus", "qwen-audio-3.0-tts-flash",
		"qwen-audio-3.1-tts-plus", "qwen-audio-3.1-tts-flash",
		"qwen-audio-3.0-realtime-plus", "qwen-audio-3.0-realtime-flash",
		"qwen-audio-3.1-realtime-plus", "qwen-audio-3.1-realtime-flash":
		return true
	}
	return false
}

func supportsClone(model string) bool {
	switch model {
	case "cosyvoice-v2", "cosyvoice-v3-plus", "cosyvoice-v3-flash", "cosyvoice-v3.5-plus", "cosyvoice-v3.5-flash":
		return true
	}
	return qwenModel(model)
}

func supportsDesign(model string) bool {
	return supportsClone(model) && model != "cosyvoice-v2" && !strings.Contains(model, "-realtime-")
}

func supportsPreprocess(model string) bool {
	return supportsClone(model) && model != "cosyvoice-v2" && model != "cosyvoice-v3-plus"
}

func languages(model string) []string {
	base := []string{"zh", "en", "fr", "de", "ja", "ko", "ru"}
	switch {
	case model == "cosyvoice-v3-plus":
		return base
	case qwenModel(model):
		return append(base, "pt", "th", "id", "vi", "it", "es", "ms", "fil", "ar")
	case supportsPreprocess(model):
		return append(base, "pt", "th", "id", "vi")
	default:
		return nil
	}
}

type CloneOptions struct {
	LanguageHint              string
	MaxPromptAudioLength      *float64 // seconds, 3..30
	EnablePreprocess          *bool
	EnableVolumeNormalization *bool // native string-valued flag
}

type CloneRequest struct {
	TargetModel string
	Prefix      string
	Audio       AudioSource
	Options     CloneOptions
}

func (r CloneRequest) input() (map[string]any, error) {
	if !supportsClone(r.TargetModel) {
		return nil, unsupported("target model is not documented for voice enrollment")
	}
	if !validPrefix(r.Prefix) {
		return nil, invalid("prefix must contain 1 to 10 ASCII letters or digits")
	}
	audio, err := r.Audio.wire()
	if err != nil {
		return nil, err
	}
	in := map[string]any{"action": "create_voice", "target_model": r.TargetModel, "prefix": r.Prefix, "audio": audio}
	o := r.Options
	if o.LanguageHint != "" {
		if !slices.Contains(languages(r.TargetModel), o.LanguageHint) {
			return nil, unsupported("language hint is not documented for the target model")
		}
		in["language_hints"] = []string{o.LanguageHint}
	}
	if o.MaxPromptAudioLength != nil || o.EnablePreprocess != nil {
		if !supportsPreprocess(r.TargetModel) {
			return nil, unsupported("audio preprocessing options are not documented for the target model")
		}
	}
	if v := o.MaxPromptAudioLength; v != nil {
		if math.IsNaN(*v) || *v < 3 || *v > 30 {
			return nil, invalid("MaxPromptAudioLength must be within [3, 30] seconds")
		}
		in["max_prompt_audio_length"] = *v
	}
	if o.EnablePreprocess != nil {
		in["enable_preprocess"] = *o.EnablePreprocess
	}
	if o.EnableVolumeNormalization != nil {
		in["enable_volume_normalization"] = strconv.FormatBool(*o.EnableVolumeNormalization)
	}
	return in, nil
}

// Voice describes a native custom voice. Status can be absent on creation;
// call Query to check whether the voice passed review.
type Voice struct {
	VoiceID      string `json:"voice_id"`
	TargetModel  string `json:"target_model"`
	Status       string `json:"status"`
	CreatedAt    string `json:"gmt_create"`
	ModifiedAt   string `json:"gmt_modified"`
	ResourceLink string `json:"resource_link"`
	VoicePrompt  string `json:"voice_prompt"`
	PreviewText  string `json:"preview_text"`
}

func (v Voice) State() voice.State {
	switch v.Status {
	case "OK":
		return voice.Ready
	case "DEPLOYING":
		return voice.Processing
	case "UNDEPLOYED":
		return voice.Rejected
	default:
		return voice.Unknown
	}
}

type Result struct {
	Metadata
	Voice
}

func (c *Client) Clone(ctx context.Context, req CloneRequest) (Result, error) {
	in, err := req.input()
	if err != nil {
		return Result{}, err
	}
	var v Voice
	meta, err := c.call(ctx, in, nil, &v)
	if err != nil {
		return Result{}, err
	}
	if !validID(v.VoiceID) {
		return Result{}, protocol("create_voice", "missing voice ID")
	}
	if v.TargetModel == "" {
		v.TargetModel = req.TargetModel
	} else if v.TargetModel != req.TargetModel {
		return Result{Metadata: meta, Voice: v}, protocol("create_voice", "target model mismatch")
	}
	return Result{Metadata: meta, Voice: v}, nil
}

type ListRequest struct {
	Prefix    string
	PageIndex *int
	PageSize  *int
}

type ListResult struct {
	Metadata
	Voices []Voice
}

func (c *Client) List(ctx context.Context, req ListRequest) (ListResult, error) {
	in := map[string]any{"action": "list_voice"}
	if req.Prefix != "" {
		if !validPrefix(req.Prefix) {
			return ListResult{}, invalid("invalid prefix")
		}
		in["prefix"] = req.Prefix
	}
	if req.PageIndex != nil {
		if *req.PageIndex < 0 {
			return ListResult{}, invalid("negative PageIndex")
		}
		in["page_index"] = *req.PageIndex
	}
	if req.PageSize != nil {
		if *req.PageSize <= 0 {
			return ListResult{}, invalid("PageSize must be positive")
		}
		in["page_size"] = *req.PageSize
	}
	var out struct {
		Voices *[]Voice `json:"voice_list"`
	}
	meta, err := c.call(ctx, in, nil, &out)
	if err != nil {
		return ListResult{}, err
	}
	if out.Voices == nil {
		return ListResult{}, protocol("list_voice", "missing voice_list")
	}
	for _, v := range *out.Voices {
		if !validID(v.VoiceID) {
			return ListResult{}, protocol("list_voice", "invalid voice ID")
		}
	}
	return ListResult{Metadata: meta, Voices: *out.Voices}, nil
}

func (c *Client) Query(ctx context.Context, id string) (Result, error) {
	if !validID(id) {
		return Result{}, invalid("invalid voice ID")
	}
	var v Voice
	meta, err := c.call(ctx, map[string]any{"action": "query_voice", "voice_id": id}, nil, &v)
	if err != nil {
		return Result{}, err
	}
	if v.Status == "" || v.VoiceID != "" && v.VoiceID != id {
		return Result{}, protocol("query_voice", "missing status or voice ID mismatch")
	}
	// The documented clone query does not return voice_id.
	v.VoiceID = id
	return Result{Metadata: meta, Voice: v}, nil
}

// Update replaces the reference recording of an enrolled voice. The voice
// ID is retained; the caller should query its status after the operation.
func (c *Client) Update(ctx context.Context, id string, audio AudioSource) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, invalid("invalid voice ID")
	}
	source, err := audio.wire()
	if err != nil {
		return Metadata{}, err
	}
	var out map[string]any
	return c.call(ctx, map[string]any{"action": "update_voice", "voice_id": id, "audio": source}, nil, &out)
}

// Delete explicitly removes a custom voice. No automatic cleanup is performed.
func (c *Client) Delete(ctx context.Context, id string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, invalid("invalid voice ID")
	}
	var out map[string]any
	return c.call(ctx, map[string]any{"action": "delete_voice", "voice_id": id}, nil, &out)
}
