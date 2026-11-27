package tencent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/voice"
)

func TestTC3Signature(t *testing.T) {
	got := authorization("test-id", "test-key", "vrs.tencentcloudapi.com", "GetTrainingText", time.Unix(1795737600, 0), []byte(`{"TaskType":5}`))
	const want = "TC3-HMAC-SHA256 Credential=test-id/2026-11-27/vrs/tc3_request, SignedHeaders=content-type;host;x-tc-action, Signature=d4b1f54404c109b4fb9d708cac816ded813536109503684050add3b81eb6f5da"
	if got != want {
		t.Fatalf("authorization = %s", got)
	}
}

func TestVoiceLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.Header.Get("X-TC-Action")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		ts, err := strconv.ParseInt(r.Header.Get("X-TC-Timestamp"), 10, 64)
		if err != nil || r.Method != http.MethodPost || r.URL.Path != "/" ||
			r.Header.Get("X-TC-Version") != version || r.Header.Get("Content-Type") != contentType ||
			r.Header.Get("X-TC-Token") != "token" || r.Header.Get("X-TC-Region") != "ap-guangzhou" ||
			r.Header.Get("Authorization") != authorization("test-id", "test-key", r.Host, action, time.Unix(ts, 0), raw) {
			t.Error("invalid signed request")
		}
		var in map[string]any
		if err := json.Unmarshal(raw, &in); err != nil {
			t.Error(err)
			return
		}
		check := func(key string, want any) {
			if in[key] != want {
				t.Errorf("%s: %s = %v, want %v", action, key, in[key], want)
			}
		}
		var out string
		switch action {
		case "GetTrainingText":
			check("TaskType", float64(5))
			check("Domain", float64(0))
			check("TextLanguage", float64(1))
			out = `{"TrainingTextList":[{"TextId":"text","Text":"测试文本"}]}`
		case "DetectEnvAndSoundQuality":
			check("TextId", "text")
			check("AudioData", "AQID")
			check("SampleRate", float64(24000))
			check("Codec", "wav")
			check("TaskType", float64(5))
			switch in["TypeId"] {
			case float64(1):
				out = `{"DetectionCode":-3,"DetectionMsg":"noise","AudioId":null}`
			case float64(2):
				out = `{"DetectionCode":0,"AudioId":"audio","DetectionTip":[{"Word":"你","Tag":0,"PronAccuracy":0,"PronFluency":null}]}`
			default:
				t.Error("invalid detection type")
			}
		case "CreateVRSTask":
			check("SessionId", "session")
			check("VoiceName", "角色")
			check("VoiceGender", float64(1))
			check("VoiceLanguage", float64(1))
			check("TaskType", float64(5))
			check("EngineType", "YTLX_V2")
			check("EnableVoiceEnhance", float64(0))
			if ids, ok := in["AudioIdList"].([]any); !ok || len(ids) != 1 || ids[0] != "audio" {
				t.Errorf("audio IDs = %v", in["AudioIdList"])
			}
			out = `{"TaskId":"task"}`
		case "DescribeVRSTaskStatus":
			check("TaskId", "task")
			out = `{"TaskId":"task","Status":2,"StatusStr":"success","VoiceType":200000000,"FastVoiceType":"fast-id","ExpireTime":"2027-11-27T00:00:00+08:00"}`
		case "GetVRSVoiceTypeInfo":
			check("VoiceType", float64(200000000))
			check("TaskType", float64(5))
			check("FastVoiceType", "fast-id")
			out = `{"VoiceType":200000000,"FastVoiceType":"fast-id","IsDeployed":true,"TaskID":"task","TaskType":5,"VoiceName":"角色","ExpireTime":"2027-11-27T00:00:00+08:00"}`
		case "GetVRSVoiceTypes":
			check("TaskType", float64(5))
			out = `{"VoiceTypeList":[{"VoiceType":200000000,"FastVoiceType":"fast-id","IsDeployed":false},{"VoiceType":200000000,"FastVoiceType":"pending","IsDeployed":null}]}`
		default:
			t.Errorf("unexpected action %s", action)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"Response":{"RequestId":"request","Data":` + out + `}}`))
	}))
	defer server.Close()
	c, err := New(Config{SecretID: "test-id", SecretKey: "test-key", Token: "token", Region: "ap-guangzhou", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	zero, one := 0, 1
	text, err := c.GetTrainingText(ctx, TrainingTextRequest{TaskType: Fast, Domain: &zero, TextLanguage: &one})
	if err != nil || len(text.Texts) != 1 || text.Texts[0].TextID != "text" || text.RequestID != "request" {
		t.Fatalf("texts = %+v, %v", text, err)
	}
	req := DetectionRequest{TextID: "text", Audio: []byte{1, 2, 3}, Type: Environment, Format: WAV, SampleRate: 24000, TaskType: Fast}
	d, err := c.Detect(ctx, req)
	if err != nil || d.Passed() || d.Code == nil || *d.Code != -3 {
		t.Fatalf("environment = %+v, %v", d, err)
	}
	req.Type = SoundQuality
	d, err = c.Detect(ctx, req)
	if err != nil || !d.Passed() || d.AudioID != "audio" || len(d.Tips) != 1 ||
		d.Tips[0].PronAccuracy == nil || *d.Tips[0].PronAccuracy != 0 || d.Tips[0].PronFluency != nil {
		t.Fatalf("quality = %+v, %v", d, err)
	}
	cloned, err := c.Clone(ctx, CloneRequest{SessionID: "session", VoiceName: "角色", VoiceGender: 1, VoiceLanguage: 1,
		AudioIDs: []string{d.AudioID}, TaskType: Fast, EngineType: "YTLX_V2", EnableVoiceEnhance: &zero})
	if err != nil || cloned.TaskID != "task" {
		t.Fatalf("clone = %+v, %v", cloned, err)
	}
	task, err := c.QueryTask(ctx, cloned.TaskID)
	if err != nil || task.State() != voice.Trained || task.State().Usable() || task.VoiceType == nil ||
		task.FastVoiceType != "fast-id" || task.ExpireTime == "" {
		t.Fatalf("task = %+v, %v", task, err)
	}
	v, err := c.Query(ctx, VoiceRef{VoiceType: *task.VoiceType, TaskType: Fast, FastVoiceType: task.FastVoiceType})
	if err != nil || !v.State().Usable() || v.TaskID != "task" {
		t.Fatalf("voice = %+v, %v", v, err)
	}
	list, err := c.List(ctx, Fast)
	if err != nil || len(list.Voices) != 2 || list.Voices[0].IsDeployed == nil ||
		*list.Voices[0].IsDeployed || list.Voices[1].IsDeployed != nil || list.Voices[0].State().Usable() {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestVoiceValidation(t *testing.T) {
	c := &Client{}
	ctx := context.Background()
	for _, r := range []DetectionRequest{
		{TextID: "text", Audio: []byte{1}, Type: 0},
		{TextID: "text", Audio: []byte{1}, Type: SoundQuality, SampleRate: 24000},
		{TextID: "text", Audio: make([]byte, 2_000_001), Type: SoundQuality, TaskType: Fast},
	} {
		if _, err := c.Detect(ctx, r); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("invalid detect = %v", err)
		}
	}
	r := CloneRequest{SessionID: "session", VoiceName: "name", VoiceGender: 1, VoiceLanguage: 1, AudioIDs: []string{"audio"}, TaskType: Fast}
	for _, mutate := range []func(*CloneRequest){
		func(r *CloneRequest) { r.AudioIDs = []string{"one", "two"} },
		func(r *CloneRequest) { r.CallbackURL = "https://example.com/callback" },
		func(r *CloneRequest) { r.VPRAudioID = "other" },
		func(r *CloneRequest) { r.EngineType = "invalid" },
	} {
		bad := r
		mutate(&bad)
		if _, err := c.Clone(ctx, bad); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
			t.Fatalf("invalid clone = %v", err)
		}
	}
	if _, err := c.Query(ctx, VoiceRef{VoiceType: 200000000, TaskType: Fast}); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatal(err)
	}
}

func TestRemoteErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body string
		code speecherr.Code
	}{
		{200, `{"Response":{"RequestId":"trace","Error":{"Code":"AuthFailure.SignatureFailure","Message":"denied"}}}`, speecherr.CodeUnauthorized},
		{200, `{"Response":{"Error":{"Code":"RequestLimitExceeded"}}}`, speecherr.CodeRateLimited},
		{200, `{"Response":{"Error":{"Code":"InvalidParameter"}}}`, speecherr.CodeInvalidArgument},
		{200, `{"Response":{"Data":null}}`, speecherr.CodeProtocol},
		{502, "bad gateway", speecherr.CodeUnavailable},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c, err := New(Config{SecretID: "id", SecretKey: "key", Endpoint: server.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.List(context.Background(), Fast)
		server.Close()
		var e *speecherr.Error
		if !speecherr.IsCode(err, tc.code) || !errors.As(err, &e) || e.Retryable {
			t.Fatalf("response %d = %v", tc.status, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, err := New(Config{SecretID: "id", SecretKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(ctx, Fast); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}

func TestNoRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unexpected redirect")
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c, err := New(Config{SecretID: "id", SecretKey: "key", Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(context.Background(), Fast); err == nil {
		t.Fatal("redirect accepted")
	}
}

func TestNullableTaskState(t *testing.T) {
	var task TaskResult
	if err := json.NewDecoder(strings.NewReader(`{"Status":null,"VoiceType":null}`)).Decode(&task); err != nil || task.State() != voice.Unknown || task.VoiceType != nil {
		t.Fatalf("unknown task = %+v, %v", task, err)
	}
}
