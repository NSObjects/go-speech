package aliyun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/voice"
)

func TestVoiceLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != customizationPath || r.Header.Get("Authorization") != "Bearer test" ||
			r.Header.Get("Content-Type") != "application/json" {
			t.Error("invalid request")
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if req.Model != "voice-enrollment" {
			t.Errorf("model = %s", req.Model)
		}
		in := req.Input
		out := ""
		switch in["action"] {
		case "create_voice":
			if _, design := in["voice_prompt"]; design {
				if in["target_model"] != "cosyvoice-v3.5-plus" || in["prefix"] != "story" ||
					in["preview_text"] != "欢迎收听今天的故事，我们一起探索奇妙的世界。" ||
					req.Parameters["sample_rate"] != float64(24000) || req.Parameters["response_format"] != "wav" {
					t.Errorf("design = %+v", req)
				}
				if _, ok := in["audio"]; ok {
					t.Error("design contains sample")
				}
				out = `{"voice_id":"design-1","target_model":"cosyvoice-v3.5-plus","preview_audio":{"data":"AQID","sample_rate":24000,"response_format":"wav"}}`
			} else {
				if in["target_model"] != "cosyvoice-v3-flash" || in["prefix"] != "test" ||
					in["audio"] != "data:audio/wav;base64,AQID" || in["enable_preprocess"] != false ||
					in["enable_volume_normalization"] != "false" || in["max_prompt_audio_length"] != float64(3) {
					t.Errorf("clone = %+v", req)
				}
				hints, ok := in["language_hints"].([]any)
				if !ok || len(hints) != 1 || hints[0] != "zh" {
					t.Errorf("hints = %v", in["language_hints"])
				}
				if _, exists := in["url"]; exists {
					t.Error("deprecated url field sent")
				}
				out = `{"voice_id":"clone-1"}`
			}
		case "list_voice":
			if in["page_index"] != float64(0) || in["page_size"] != float64(10) || in["prefix"] != "test" {
				t.Errorf("list = %+v", req)
			}
			out = `{"voice_list":[{"voice_id":"clone-1","status":"DEPLOYING","target_model":"cosyvoice-v3-flash"},{"voice_id":"design-1","status":"UNDEPLOYED","voice_prompt":"沉稳"}]}`
		case "query_voice":
			if in["voice_id"] != "clone-1" {
				t.Errorf("query = %+v", req)
			}
			out = `{"status":"OK","target_model":"cosyvoice-v3-flash","resource_link":"https://example.com/sample.wav","gmt_create":"2026-11-27 09:00:00"}`
		case "update_voice":
			if in["voice_id"] != "clone-1" || in["audio"] != "https://example.com/new.wav" {
				t.Errorf("update = %+v", req)
			}
			out = "{}"
		case "delete_voice":
			if in["voice_id"] != "clone-1" {
				t.Errorf("delete = %+v", req)
			}
			out = "{}"
		default:
			t.Errorf("unexpected action: %v", in)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"request_id":"req-id","usage":{"count":1},"output":` + out + `}`))
	}))
	defer server.Close()
	c, err := New(Config{APIKey: "test", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	off, length, zero, size := false, 3.0, 0, 10
	r, err := c.Clone(ctx, CloneRequest{
		TargetModel: "cosyvoice-v3-flash", Prefix: "test",
		Audio: AudioSource{Data: []byte{1, 2, 3}, Format: WAV},
		Options: CloneOptions{LanguageHint: "zh", MaxPromptAudioLength: &length, EnablePreprocess: &off, EnableVolumeNormalization: &off},
	})
	if err != nil || r.VoiceID != "clone-1" || r.TargetModel != "cosyvoice-v3-flash" ||
		r.State() != voice.Unknown || r.RequestID != "req-id" || r.Usage == nil || r.Usage.Count == nil || *r.Usage.Count != 1 {
		t.Fatalf("clone = %+v, %v", r, err)
	}
	d, err := c.Design(ctx, DesignRequest{
		TargetModel: "cosyvoice-v3.5-plus", Prefix: "story", VoicePrompt: "沉稳自然",
		PreviewText: "欢迎收听今天的故事，我们一起探索奇妙的世界。", SampleRate: 24000, ResponseFormat: "wav",
	})
	if err != nil || d.VoiceID != "design-1" || string(d.Preview.Data) != string([]byte{1, 2, 3}) || d.State().Usable() {
		t.Fatalf("design = %+v, %v", d, err)
	}
	l, err := c.List(ctx, ListRequest{Prefix: "test", PageIndex: &zero, PageSize: &size})
	if err != nil || len(l.Voices) != 2 || l.Voices[0].State() != voice.Processing || l.Voices[1].State() != voice.Rejected {
		t.Fatalf("list = %+v, %v", l, err)
	}
	r, err = c.Query(ctx, "clone-1")
	if err != nil || r.VoiceID != "clone-1" || !r.State().Usable() || r.ResourceLink == "" {
		t.Fatalf("query = %+v, %v", r, err)
	}
	if m, err := c.Update(ctx, "clone-1", AudioSource{URL: "https://example.com/new.wav"}); err != nil || m.RequestID != "req-id" {
		t.Fatalf("update = %+v, %v", m, err)
	}
	if m, err := c.Delete(ctx, "clone-1"); err != nil || m.RequestID != "req-id" {
		t.Fatalf("delete = %+v, %v", m, err)
	}
}

func TestVoiceValidation(t *testing.T) {
	on, negative, tooLong := true, -1, 31.0
	c := &Client{}
	ctx := context.Background()
	a := AudioSource{Data: []byte{1}, Format: WAV}
	for _, req := range []CloneRequest{
		{TargetModel: "invalid", Prefix: "test", Audio: a},
		{TargetModel: "cosyvoice-v3-flash", Prefix: "a-b", Audio: a},
		{TargetModel: "cosyvoice-v3-flash", Prefix: "test", Audio: AudioSource{Data: []byte{1}, URL: "https://example.com/sample", Format: WAV}},
		{TargetModel: "cosyvoice-v2", Prefix: "test", Audio: a, Options: CloneOptions{EnablePreprocess: &on}},
		{TargetModel: "cosyvoice-v3-plus", Prefix: "test", Audio: a, Options: CloneOptions{LanguageHint: "pt"}},
		{TargetModel: "cosyvoice-v3-flash", Prefix: "test", Audio: a, Options: CloneOptions{MaxPromptAudioLength: &tooLong}},
	} {
		if _, err := c.Clone(ctx, req); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) && !speecherr.IsCode(err, speecherr.CodeUnsupported) {
			t.Fatalf("invalid request = %v", err)
		}
	}
	if _, err := c.List(ctx, ListRequest{PageIndex: &negative}); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatal(err)
	}
	for _, req := range []DesignRequest{
		{TargetModel: "cosyvoice-v2", Prefix: "test", VoicePrompt: "voice", PreviewText: strings.Repeat("你", 15)},
		{TargetModel: "cosyvoice-v3-flash", Prefix: "test", VoicePrompt: "voice", PreviewText: "short"},
		{TargetModel: "cosyvoice-v3-flash", Prefix: "test", VoicePrompt: "voice", PreviewText: strings.Repeat("你", 15), SampleRate: 8000},
	} {
		if _, err := c.Design(ctx, req); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) && !speecherr.IsCode(err, speecherr.CodeUnsupported) {
			t.Fatalf("invalid design = %v", err)
		}
	}
}

func TestVoiceErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		code   speecherr.Code
	}{
		{401, `{"code":"InvalidApiKey","message":"denied","request_id":"trace"}`, speecherr.CodeUnauthorized},
		{429, `{"code":"Throttling","request_id":"trace"}`, speecherr.CodeRateLimited},
		{200, `{"code":"InvalidParameter","request_id":"trace"}`, speecherr.CodeInvalidArgument},
		{200, `{"output":null}`, speecherr.CodeProtocol},
		{200, `{"output":{}}`, speecherr.CodeProtocol},
		{500, "unavailable", speecherr.CodeUnavailable},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c, err := New(Config{APIKey: "test", Endpoint: server.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Query(context.Background(), "id")
		server.Close()
		if !speecherr.IsCode(err, tc.code) {
			t.Fatalf("status %d: %v", tc.status, err)
		}
		var e *speecherr.Error
		if !errors.As(err, &e) || e.Retryable {
			t.Fatalf("unexpected retryable error: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, err := New(Config{APIKey: "test", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Query(ctx, "id"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}

func TestDesignPreservesCreatedIDOnBadPreview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"request_id":"req","output":{"voice_id":"created","preview_audio":{"data":"not base64","sample_rate":24000,"response_format":"wav"}}}`))
	}))
	defer server.Close()
	c, err := New(Config{APIKey: "test", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Design(context.Background(), DesignRequest{
		TargetModel: "cosyvoice-v3-flash", Prefix: "test", VoicePrompt: "voice", PreviewText: strings.Repeat("你", 15),
	})
	if !speecherr.IsCode(err, speecherr.CodeProtocol) || r.VoiceID != "created" || r.RequestID != "req" {
		t.Fatalf("creation metadata lost: %+v, %v", r, err)
	}
}

func TestVoiceNoRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unexpected redirect")
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
	if _, err := c.Delete(context.Background(), "id"); err == nil {
		t.Fatal("redirect treated as success")
	}
}

func TestCloneModelMismatchPreservesCreatedID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"request_id":"trace","output":{"voice_id":"created","target_model":"cosyvoice-v3-plus"}}`))
	}))
	defer server.Close()
	c, err := New(Config{APIKey: "test", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Clone(context.Background(), CloneRequest{
		TargetModel: "cosyvoice-v3-flash", Prefix: "test",
		Audio: AudioSource{Data: []byte{1}, Format: WAV},
	})
	if !speecherr.IsCode(err, speecherr.CodeProtocol) || r.VoiceID != "created" || r.RequestID != "trace" || r.TargetModel != "cosyvoice-v3-plus" {
		t.Fatalf("creation response lost: %+v, %v", r, err)
	}
}
