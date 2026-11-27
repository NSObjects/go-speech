// Package volcengine implements Seed-TTS 3.0 voice enrollment, query and
// design. It performs one HTTP request per operation and does not activate,
// poll, download previews, or synthesize voices automatically.
package volcengine

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/voice"
)

const maxSampleBytes = 10_000_000

// VoiceRef selects either an existing slot ID or a custom postpaid name.
// Exactly one field must be set.
type VoiceRef struct {
	SpeakerID       string
	CustomSpeakerID string
}

type wireRef struct {
	SpeakerID       string `json:"speaker_id"`
	CustomSpeakerID string `json:"custom_speaker_id,omitempty"`
}

func (r VoiceRef) wire() (wireRef, error) {
	if (r.SpeakerID == "") == (r.CustomSpeakerID == "") {
		return wireRef{}, invalid("provide exactly one speaker ID or custom speaker ID")
	}
	if r.CustomSpeakerID != "" {
		if !validCustomID(r.CustomSpeakerID) {
			return wireRef{}, invalid("invalid custom speaker ID")
		}
		return wireRef{SpeakerID: "custom_speaker_id", CustomSpeakerID: r.CustomSpeakerID}, nil
	}
	if !validID(r.SpeakerID) || r.SpeakerID == "custom_speaker_id" {
		return wireRef{}, invalid("invalid speaker ID")
	}
	return wireRef{SpeakerID: r.SpeakerID}, nil
}

func validID(s string) bool {
	return s != "" && len(s) <= 512 && utf8.ValidString(s) &&
		strings.TrimSpace(s) == s && !strings.ContainsAny(s, "/\\\r\n\t ")
}

func validCustomID(s string) bool {
	letter := func(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }
	alnum := func(b byte) bool { return letter(b) || b >= '0' && b <= '9' }
	if len(s) < 8 || len(s) > 256 || !letter(s[0]) || !alnum(s[len(s)-1]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !alnum(s[i]) && s[i] != '_' && s[i] != '-' {
			return false
		}
	}
	if s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z' && s[2] == '_' {
		return false
	}
	lower := strings.ToLower(s)
	for _, p := range []string{"bv", "s_", "icl_", "mix_", "dit_", "wvae_", "moon_", "mercury_", "venus_", "earth_", "mars_", "jupiter_", "saturn_", "uranus_", "neptune_", "pluto_", "umm_"} {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	for _, suffix := range []string{"_bigtts", "_bigtts_cc", "_tob", "_cs_tob", "_streaming"} {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}
	return true
}

// SampleFormat names the native enrollment file formats. PCM must already
// be 24 kHz mono. The caller provides a 10–60 second recording; the SDK
// checks byte size and format but does not decode or inspect duration.
type SampleFormat string

const (
	WAV SampleFormat = "wav"
	MP3 SampleFormat = "mp3"
	OGG SampleFormat = "ogg"
	M4A SampleFormat = "m4a"
	AAC SampleFormat = "aac"
	PCM SampleFormat = "pcm"
)

type CloneOptions struct {
	PromptText                 string `json:"prompt_text,omitempty"`
	ProsodyBoost               *bool  `json:"prosody_boost,omitempty"`
	DemoText                   string `json:"demo_text,omitempty"`
	EnableAudioDenoise         *bool  `json:"enable_audio_denoise,omitempty"`
	DisableVolumeNormalization *bool  `json:"disable_volume_normalization,omitempty"`
}

type CloneRequest struct {
	Voice    VoiceRef
	Audio    []byte
	Format   SampleFormat
	Language *int // native language code; nil keeps provider default
	Options  *CloneOptions
}

type sample struct {
	Data   []byte       `json:"data"`
	Format SampleFormat `json:"format"`
}

type cloneWire struct {
	wireRef
	Audio       sample        `json:"audio"`
	Language    *int          `json:"language,omitempty"`
	ExtraParams *CloneOptions `json:"extra_params,omitempty"`
}

func (r CloneRequest) wire() (cloneWire, error) {
	ref, err := r.Voice.wire()
	if err != nil {
		return cloneWire{}, err
	}
	if len(r.Audio) == 0 || len(r.Audio) > maxSampleBytes {
		return cloneWire{}, invalid("audio must be nonempty and at most 10 MB")
	}
	switch r.Format {
	case WAV, MP3, OGG, M4A, AAC, PCM:
	default:
		return cloneWire{}, invalid("unsupported sample format")
	}
	if r.Language != nil && *r.Language < 0 {
		return cloneWire{}, invalid("invalid language code")
	}
	if o := r.Options; o != nil {
		if !utf8.ValidString(o.PromptText) || (o.DemoText != "" && !validText(o.DemoText, 4, 300)) {
			return cloneWire{}, invalid("invalid prompt or demo text")
		}
		if o.ProsodyBoost != nil && *o.ProsodyBoost && r.Language == nil {
			return cloneWire{}, invalid("prosody boost requires explicit source language")
		}
	}
	return cloneWire{wireRef: ref, Audio: sample{Data: r.Audio, Format: r.Format}, Language: r.Language, ExtraParams: r.Options}, nil
}

// ImagePrompt accepts a remotely accessible URL or raw image bytes, not both.
// Bytes are base64-encoded by encoding/json. The SDK never fetches the URL.
type ImagePrompt struct {
	URL   string `json:"image_url,omitempty"`
	Bytes []byte `json:"image_bytes,omitempty"`
}

type DesignRequest struct {
	SpeakerID  string
	Text       string
	TextPrompt string
	Image      *ImagePrompt
	Language   *int
}

type designPrompt struct {
	Text  string       `json:"text_prompt,omitempty"`
	Image *ImagePrompt `json:"image_prompt,omitempty"`
}

type designWire struct {
	SpeakerID string       `json:"speaker_id"`
	Text      string       `json:"text"`
	Prompt    designPrompt `json:"prompt"`
	Language  *int         `json:"language,omitempty"`
}

func validText(s string, min, max int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" &&
		utf8.RuneCountInString(s) >= min && utf8.RuneCountInString(s) <= max
}

func (r DesignRequest) wire() (designWire, error) {
	if !validID(r.SpeakerID) || !strings.HasPrefix(r.SpeakerID, "S_") || len(r.SpeakerID) <= 2 {
		return designWire{}, invalid("voice design requires a prepaid S_ speaker ID")
	}
	if !validText(r.Text, 1, 300) || (r.TextPrompt != "" && !validText(r.TextPrompt, 1, 200)) {
		return designWire{}, invalid("invalid preview text or text prompt")
	}
	if r.TextPrompt == "" && r.Image == nil {
		return designWire{}, invalid("at least one design prompt is required")
	}
	if r.Language != nil && *r.Language != 0 && *r.Language != 1 {
		return designWire{}, invalid("design language must be 0 or 1")
	}
	if p := r.Image; p != nil {
		if (p.URL == "") == (len(p.Bytes) == 0) || len(p.Bytes) > maxSampleBytes {
			return designWire{}, invalid("provide one image URL or up to 10 MB of image bytes")
		}
		if p.URL != "" {
			u, err := url.Parse(p.URL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
				return designWire{}, invalid("invalid image URL")
			}
		}
	}
	return designWire{SpeakerID: r.SpeakerID, Text: r.Text, Prompt: designPrompt{Text: r.TextPrompt, Image: r.Image}, Language: r.Language}, nil
}

// ModelStatus preserves native model identity and an expiring preview URL.
type ModelStatus struct {
	ModelType int    `json:"model_type"`
	DemoAudio string `json:"demo_audio"`
}

// Result preserves native response fields. Preview URLs expire; the caller
// decides whether to retrieve them. Missing counters and status remain nil.
type Result struct {
	SpeakerID              string        `json:"speaker_id"`
	Status                 *int          `json:"status"`
	AvailableTrainingTimes *int          `json:"available_training_times"`
	CreateTimeMS           *int64        `json:"create_time"`
	Language               *int          `json:"language"`
	SpeakerStatus          []ModelStatus `json:"speaker_status"`
	DemoAudio              string        `json:"demo_audio"`
	LogID                  string        `json:"-"`
}

func (r Result) State() voice.State {
	if r.Status != nil {
		switch *r.Status {
		case 0:
			return voice.NotFound
		case 2:
			return voice.Ready
		case 4:
			return voice.Active
		}
	}
	return voice.Unknown
}

func invalid(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeInvalidArgument, Provider: "volcengine", Operation: "voice_validate", Err: errors.New(msg)}
}

func (c *Client) Clone(ctx context.Context, req CloneRequest) (Result, error) {
	wire, err := req.wire()
	if err != nil {
		return Result{}, err
	}
	return c.call(ctx, "voice_clone", wire)
}

func (c *Client) Query(ctx context.Context, ref VoiceRef) (Result, error) {
	wire, err := ref.wire()
	if err != nil {
		return Result{}, err
	}
	return c.call(ctx, "get_voice", wire)
}

func (c *Client) Design(ctx context.Context, req DesignRequest) (Result, error) {
	wire, err := req.wire()
	if err != nil {
		return Result{}, err
	}
	return c.call(ctx, "voice_design", wire)
}
