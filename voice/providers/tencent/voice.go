package tencent

import (
	"context"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/NSObjects/go-speech/voice"
)

// TaskType uses native request values. Basic/default is omitted by training
// APIs; list and detail use zero for non-fast voices.
type TaskType int

const (
	Basic TaskType = 0
	Fast  TaskType = 5
)

func (t TaskType) valid() bool { return t == Basic || t == Fast }

func validID(s string) bool {
	return s != "" && len(s) <= 512 && utf8.ValidString(s) &&
		strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\r\n\t ")
}

func validText(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != ""
}

type TrainingTextRequest struct {
	TaskType     TaskType `json:"TaskType,omitempty"`
	Domain       *int     `json:"Domain,omitempty"`
	TextLanguage *int     `json:"TextLanguage,omitempty"`
}

type TrainingText struct {
	TextID string `json:"TextId"`
	Text   string `json:"Text"`
}

type TrainingTextResult struct {
	Metadata
	Texts []TrainingText `json:"TrainingTextList"`
}

func (c *Client) GetTrainingText(ctx context.Context, req TrainingTextRequest) (TrainingTextResult, error) {
	if !req.TaskType.valid() || (req.TaskType != Fast && (req.Domain != nil || req.TextLanguage != nil)) ||
		req.Domain != nil && (*req.Domain < 0 || *req.Domain > 3) ||
		req.TextLanguage != nil && *req.TextLanguage != 1 {
		return TrainingTextResult{}, invalid("invalid training text options")
	}
	var result TrainingTextResult
	meta, err := c.call(ctx, "GetTrainingText", req, &result)
	if err != nil {
		return TrainingTextResult{}, err
	}
	result.Metadata = meta
	return result, nil
}

type DetectionType int

const (
	Environment  DetectionType = 1
	SoundQuality DetectionType = 2
)

type SampleFormat string

const (
	WAV SampleFormat = "wav"
	MP3 SampleFormat = "mp3"
	AAC SampleFormat = "aac"
	M4A SampleFormat = "m4a"
)

func (f SampleFormat) valid() bool {
	return f == "" || f == WAV || f == MP3 || f == AAC || f == M4A
}

type DetectionRequest struct {
	TextID     string        `json:"TextId"`
	Audio      []byte        `json:"AudioData"`
	Type       DetectionType `json:"TypeId"`
	Format     SampleFormat  `json:"Codec,omitempty"`
	SampleRate int           `json:"SampleRate,omitempty"`
	TaskType   TaskType      `json:"TaskType,omitempty"`
}

type DetectionWord struct {
	PronAccuracy *float64 `json:"PronAccuracy"`
	PronFluency  *float64 `json:"PronFluency"`
	Tag          *int     `json:"Tag"`
	Word         string   `json:"Word"`
}

type DetectionResult struct {
	Metadata
	AudioID string          `json:"AudioId"`
	Code    *int            `json:"DetectionCode"`
	Message string          `json:"DetectionMsg"`
	Tips    []DetectionWord `json:"DetectionTip"`
}

// Passed reports a positive provider detection result, not a locally
// inferred assessment. Environment checks do not return an audio ID.
func (r DetectionResult) Passed() bool { return r.Code != nil && *r.Code == 0 }

func (c *Client) Detect(ctx context.Context, req DetectionRequest) (DetectionResult, error) {
	if !req.TaskType.valid() || !validID(req.TextID) || (req.Type != Environment && req.Type != SoundQuality) ||
		len(req.Audio) == 0 || !req.Format.valid() {
		return DetectionResult{}, invalid("invalid detection request")
	}
	// One-sentence limit is 2 MB. Bound other requests below Cloud API's
	// 10 MB JSON payload limit, accounting for Base64 expansion.
	if len(req.Audio) > 7_000_000 || req.TaskType == Fast && len(req.Audio) > 2_000_000 {
		return DetectionResult{}, invalid("sample exceeds request size limit")
	}
	switch req.SampleRate {
	case 0, 16000:
	case 24000, 48000:
		if req.TaskType != Fast {
			return DetectionResult{}, invalid("24/48 kHz detection requires fast cloning")
		}
	default:
		return DetectionResult{}, invalid("invalid sample rate")
	}
	var result DetectionResult
	meta, err := c.call(ctx, "DetectEnvAndSoundQuality", req, &result)
	if err != nil {
		return DetectionResult{}, err
	}
	result.Metadata = meta
	if result.Code == nil {
		return result, protocol("DetectEnvAndSoundQuality", "missing detection code")
	}
	if result.Passed() && req.Type == SoundQuality && !validID(result.AudioID) {
		return result, protocol("DetectEnvAndSoundQuality", "missing accepted audio ID")
	}
	return result, nil
}

// CloneRequest submits previously detected audio IDs, not audio bytes.
// SessionID is supplied by the caller and should be unique for each task.
type CloneRequest struct {
	SessionID          string       `json:"SessionId"`
	VoiceName          string       `json:"VoiceName"`
	VoiceGender        int          `json:"VoiceGender"`
	VoiceLanguage      int          `json:"VoiceLanguage"`
	AudioIDs           []string     `json:"AudioIdList"`
	SampleRate         int          `json:"SampleRate,omitempty"`
	Format             SampleFormat `json:"Codec,omitempty"`
	CallbackURL        string       `json:"CallbackUrl,omitempty"`
	ModelType          int          `json:"ModelType,omitempty"`
	TaskType           TaskType     `json:"TaskType,omitempty"`
	VPRAudioID         string       `json:"VPRAudioId,omitempty"`
	EnableVoiceEnhance *int         `json:"EnableVoiceEnhance,omitempty"`
	EngineType         string       `json:"EngineType,omitempty"`
}

type CloneResult struct {
	Metadata
	TaskID string `json:"TaskId"`
}

func (c *Client) Clone(ctx context.Context, req CloneRequest) (CloneResult, error) {
	if !req.TaskType.valid() || !validID(req.SessionID) || !validText(req.VoiceName) ||
		(req.VoiceGender != 1 && req.VoiceGender != 2) || req.VoiceLanguage != 1 ||
		len(req.AudioIDs) == 0 || !req.Format.valid() ||
		(req.SampleRate != 0 && req.SampleRate != 16000) ||
		(req.ModelType != 0 && req.ModelType != 1 && req.ModelType != 2) ||
		(req.EngineType != "" && req.EngineType != "WXZL" && req.EngineType != "YTLX_V2") {
		return CloneResult{}, invalid("invalid clone request")
	}
	for _, id := range req.AudioIDs {
		if !validID(id) {
			return CloneResult{}, invalid("invalid audio ID")
		}
	}
	if req.TaskType == Fast && (len(req.AudioIDs) != 1 || req.CallbackURL != "" || req.VPRAudioID != "") {
		return CloneResult{}, invalid("fast clone requires one audio ID and does not accept callback or VPR audio")
	}
	if req.EnableVoiceEnhance != nil && (req.TaskType != Fast || (*req.EnableVoiceEnhance != 0 && *req.EnableVoiceEnhance != 1)) {
		return CloneResult{}, invalid("voice enhancement requires fast clone and value 0 or 1")
	}
	if req.VPRAudioID != "" && !validID(req.VPRAudioID) {
		return CloneResult{}, invalid("invalid VPR audio ID")
	}
	if req.CallbackURL != "" {
		u, err := url.Parse(req.CallbackURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return CloneResult{}, invalid("invalid callback URL")
		}
	}
	var result CloneResult
	meta, err := c.call(ctx, "CreateVRSTask", req, &result)
	if err != nil {
		return CloneResult{}, err
	}
	result.Metadata = meta
	if !validID(result.TaskID) {
		return result, protocol("CreateVRSTask", "missing task ID")
	}
	return result, nil
}

type TaskResult struct {
	Metadata
	TaskID        string `json:"TaskId"`
	Status        *int   `json:"Status"`
	StatusText    string `json:"StatusStr"`
	VoiceType     *int64 `json:"VoiceType"`
	FastVoiceType string `json:"FastVoiceType"`
	ErrorMessage  string `json:"ErrorMsg"`
	ExpireTime    string `json:"ExpireTime"`
}

// State describes the training task, not deployment or expiry. Use Query
// to check whether the resulting voice is deployed.
func (r TaskResult) State() voice.State {
	if r.Status == nil {
		return voice.Unknown
	}
	switch *r.Status {
	case 0, 1:
		return voice.Processing
	case 2:
		return voice.Trained
	case 3:
		return voice.Failed
	default:
		return voice.Unknown
	}
}

func (c *Client) QueryTask(ctx context.Context, id string) (TaskResult, error) {
	if !validID(id) {
		return TaskResult{}, invalid("invalid task ID")
	}
	var result TaskResult
	meta, err := c.call(ctx, "DescribeVRSTaskStatus", map[string]string{"TaskId": id}, &result)
	if err != nil {
		return TaskResult{}, err
	}
	result.Metadata = meta
	if result.TaskID != "" && result.TaskID != id {
		return result, protocol("DescribeVRSTaskStatus", "task ID mismatch")
	}
	result.TaskID = id
	return result, nil
}

type VoiceRef struct {
	VoiceType     int64    `json:"VoiceType"`
	TaskType      TaskType `json:"TaskType,omitempty"`
	FastVoiceType string   `json:"FastVoiceType,omitempty"`
}

type VoiceInfo struct {
	VoiceType     *int64 `json:"VoiceType"`
	VoiceName     string `json:"VoiceName"`
	VoiceGender   *int   `json:"VoiceGender"`
	TaskType      *int   `json:"TaskType"`
	TaskID        string `json:"TaskID"`
	DateCreated   string `json:"DateCreated"`
	IsDeployed    *bool  `json:"IsDeployed"`
	ExpireTime    string `json:"ExpireTime"`
	FastVoiceType string `json:"FastVoiceType"`
}

// State reflects the returned deployment flag, not a guarantee of access
// or absence of expiry. The caller must consider ExpireTime.
func (r VoiceInfo) State() voice.State {
	if r.IsDeployed != nil && *r.IsDeployed {
		return voice.Ready
	}
	return voice.Unknown
}

type VoiceResult struct {
	Metadata
	VoiceInfo
}

func (c *Client) Query(ctx context.Context, ref VoiceRef) (VoiceResult, error) {
	if !ref.TaskType.valid() || ref.VoiceType <= 0 ||
		ref.TaskType == Fast && (!validID(ref.FastVoiceType) || (ref.VoiceType != 200000000 && ref.VoiceType != 200000400)) ||
		ref.TaskType != Fast && ref.FastVoiceType != "" {
		return VoiceResult{}, invalid("invalid voice reference")
	}
	var result VoiceResult
	meta, err := c.call(ctx, "GetVRSVoiceTypeInfo", ref, &result)
	if err != nil {
		return VoiceResult{}, err
	}
	result.Metadata = meta
	if result.VoiceType == nil || *result.VoiceType != ref.VoiceType ||
		ref.TaskType == Fast && result.FastVoiceType != ref.FastVoiceType {
		return result, protocol("GetVRSVoiceTypeInfo", "voice ID mismatch")
	}
	return result, nil
}

type ListResult struct {
	Metadata
	Voices []VoiceInfo `json:"VoiceTypeList"`
}

func (c *Client) List(ctx context.Context, kind TaskType) (ListResult, error) {
	if !kind.valid() {
		return ListResult{}, invalid("invalid task type")
	}
	var result ListResult
	meta, err := c.call(ctx, "GetVRSVoiceTypes", map[string]TaskType{"TaskType": kind}, &result)
	if err != nil {
		return ListResult{}, err
	}
	result.Metadata = meta
	return result, nil
}
