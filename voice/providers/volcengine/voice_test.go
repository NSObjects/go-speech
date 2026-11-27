package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/voice"
)

func TestVoiceOperations(t *testing.T) {
	var mu sync.Mutex
	var ids []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Api-Request-Id")
		if r.Method != http.MethodPost || r.Header.Get("X-Api-Key") != "test" || r.Header.Get("Content-Type") != "application/json" ||
			!regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
			t.Error("invalid request headers")
		}
		mu.Lock()
		for _, previous := range ids {
			if id == previous {
				t.Error("request ID reused")
			}
		}
		ids = append(ids, id)
		mu.Unlock()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Tt-Logid", "log-id")
		switch r.URL.Path {
		case "/api/v3/tts/voice_clone":
			a, _ := body["audio"].(map[string]any)
			o, _ := body["extra_params"].(map[string]any)
			if body["speaker_id"] != "custom_speaker_id" || body["custom_speaker_id"] != "StudioVoice_01" ||
				body["language"] != float64(0) || a["data"] != "AQID" || a["format"] != "wav" ||
				o["enable_audio_denoise"] != false || o["demo_text"] != "你好世界" || o["prosody_boost"] != true {
				t.Errorf("clone body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"speaker_id":"S_test","status":2,"available_training_times":0,"language":0,"create_time":1772026663000,"speaker_status":[{"model_type":5,"demo_audio":"https://example.com/clone.wav"}]}`))
		case "/api/v3/tts/get_voice":
			if body["speaker_id"] != "S_test" || len(body) != 1 {
				t.Errorf("query body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"speaker_id":"S_test","status":4}`))
		case "/api/v3/tts/voice_design":
			p, _ := body["prompt"].(map[string]any)
			i, _ := p["image_prompt"].(map[string]any)
			if body["speaker_id"] != "S_test" || body["text"] != "你好" || p["text_prompt"] != "低沉有力" || i["image_bytes"] != "BAUG" {
				t.Errorf("design body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"speaker_id":"S_test","status":2,"demo_audio":"https://example.com/design.wav"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	c, err := New(Config{APIKey: "test", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	on, off, zero := true, false, 0
	result, err := c.Clone(ctx, CloneRequest{
		Voice: VoiceRef{CustomSpeakerID: "StudioVoice_01"}, Audio: []byte{1, 2, 3}, Format: WAV, Language: &zero,
		Options: &CloneOptions{DemoText: "你好世界", ProsodyBoost: &on, EnableAudioDenoise: &off},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State() != voice.Ready || !result.State().Usable() || result.AvailableTrainingTimes == nil || *result.AvailableTrainingTimes != 0 ||
		result.Language == nil || *result.Language != 0 || result.LogID != "log-id" || len(result.SpeakerStatus) != 1 ||
		result.SpeakerStatus[0].ModelType != 5 || result.SpeakerStatus[0].DemoAudio == "" {
		t.Fatalf("clone = %+v", result)
	}
	result, err = c.Query(ctx, VoiceRef{SpeakerID: result.SpeakerID})
	if err != nil || result.State() != voice.Active || result.AvailableTrainingTimes != nil {
		t.Fatalf("query = %+v, %v", result, err)
	}
	result, err = c.Design(ctx, DesignRequest{SpeakerID: "S_test", Text: "你好", TextPrompt: "低沉有力", Image: &ImagePrompt{Bytes: []byte{4, 5, 6}}})
	if err != nil || result.State() != voice.Ready || result.DemoAudio != "https://example.com/design.wav" {
		t.Fatalf("design = %+v, %v", result, err)
	}
}

func TestVoiceValidation(t *testing.T) {
	for _, id := range []string{"ab_voice01", "S_custom01", "BVcustom01", "StudioVoice_streaming", "short", "StudioVoice_"} {
		if validCustomID(id) {
			t.Errorf("accepted reserved or invalid ID %q", id)
		}
	}
	c := &Client{}
	ctx := context.Background()
	for _, req := range []CloneRequest{
		{},
		{Voice: VoiceRef{SpeakerID: "S_test", CustomSpeakerID: "StudioVoice_01"}, Audio: []byte{1}, Format: WAV},
		{Voice: VoiceRef{SpeakerID: "S_test"}, Audio: []byte{1}, Format: "raw"},
		{Voice: VoiceRef{SpeakerID: "S_test"}, Audio: make([]byte, maxSampleBytes+1), Format: WAV},
	} {
		if _, err := c.Clone(ctx, req); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("invalid clone: %v", err)
		}
	}
	for _, req := range []DesignRequest{
		{},
		{SpeakerID: "S_test", Text: "hello"},
		{SpeakerID: "S_test", Text: "hello", Image: &ImagePrompt{URL: "file:///tmp/image"}},
		{SpeakerID: "S_test", Text: "hello", Image: &ImagePrompt{URL: "https://example.com/a", Bytes: []byte{1}}},
	} {
		if _, err := c.Design(ctx, req); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("invalid design: %v", err)
		}
	}
}

func TestVoiceRemoteErrorsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		code   speecherr.Code
	}{
		{401, `{"code":45000001,"message":"denied"}`, speecherr.CodeUnauthorized},
		{429, `{"code":45001123,"message":"limit"}`, speecherr.CodeRateLimited},
		{500, "unavailable", speecherr.CodeUnavailable},
		{200, `{"code":45001107,"message":"missing"}`, speecherr.CodeInternal},
		{200, `{}`, speecherr.CodeProtocol},
		{200, "invalid", speecherr.CodeProtocol},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Tt-Logid", "trace")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c, err := New(Config{APIKey: "test", Endpoint: server.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Query(context.Background(), VoiceRef{SpeakerID: "S_test"})
		server.Close()
		if !speecherr.IsCode(err, tc.code) {
			t.Fatalf("status %d: %v", tc.status, err)
		}
		if tc.code != speecherr.CodeProtocol {
			var remote *RemoteError
			var speech *speecherr.Error
			if !errors.As(err, &remote) || remote.HTTPStatus != tc.status || remote.LogID != "trace" ||
				!errors.As(err, &speech) || speech.Retryable {
				t.Fatalf("diagnostics lost: %v", err)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, err := New(Config{APIKey: "test", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Query(ctx, VoiceRef{SpeakerID: "S_test"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled = %v", err)
	}
}

func TestNoRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("voice request followed redirect")
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c, err := New(Config{APIKey: "test", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Query(context.Background(), VoiceRef{SpeakerID: "S_test"}); err == nil {
		t.Fatal("redirect treated as success")
	}
}

func TestCloneLanguageRules(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		code int
		prompt string
		boost *bool
		wantError bool
	}{
		{0, "", &on, false},
		{2, "", &on, false},
		{25, "", &on, false},
		{62, "", &on, true},
		{62, "", &off, false},
		{22, "", nil, true},
		{22, " ", nil, true},
		{22, "sample transcript", &off, false},
		{22, "sample transcript", &on, true},
		{86, "", &on, false},
		{118, "", nil, true},
		{118, "sample transcript", nil, false},
		{200, "", nil, false},
	} {
		r := CloneRequest{Voice: VoiceRef{SpeakerID: "S_test"}, Audio: []byte{1}, Language: &tc.code,
			Options: &CloneOptions{PromptText: tc.prompt, ProsodyBoost: tc.boost}}
		_, err := r.wire()
		if (err != nil) != tc.wantError {
			t.Errorf("language %d: got %v; want error=%t", tc.code, err, tc.wantError)
		}
	}
	r := CloneRequest{Voice: VoiceRef{SpeakerID: "S_test"}, Audio: []byte{1}}
	w, err := r.wire()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Audio map[string]any `json:"audio"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body.Audio["format"]; ok {
		t.Fatal("unspecified format must be omitted")
	}
}

func TestExplicitSlotMismatchPreservesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tt-Logid", "trace")
		_, _ = w.Write([]byte(`{"speaker_id":"S_other","status":2}`))
	}))
	defer server.Close()
	c, err := New(Config{APIKey: "test", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ref := VoiceRef{SpeakerID: "S_test"}
	check := func(r Result, err error) {
		t.Helper()
		if !speecherr.IsCode(err, speecherr.CodeProtocol) || r.SpeakerID != "S_other" || r.LogID != "trace" {
			t.Fatalf("mismatch lost result or error: %+v %v", r, err)
		}
	}
	check(c.Query(ctx, ref))
	check(c.Clone(ctx, CloneRequest{Voice: ref, Audio: []byte{1}, Format: WAV}))
	check(c.Design(ctx, DesignRequest{SpeakerID: "S_test", Text: "hello", TextPrompt: "calm"}))
	if _, err := c.Query(ctx, VoiceRef{CustomSpeakerID: "StudioVoice_01"}); err != nil {
		t.Fatalf("custom name resolution rejected: %v", err)
	}
}
