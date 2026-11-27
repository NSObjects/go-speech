# Tencent VRS

The independent VRS client uses Cloud API TC3 signing and supports the six documented public operations. Use SecretID and SecretKey, optionally a temporary Token; no AppID is required.

```go
c, err := tencent.New(tencent.Config{SecretID: secretID, SecretKey: secretKey})
if err != nil {
    return err
}
texts, err := c.GetTrainingText(ctx, tencent.TrainingTextRequest{TaskType: tencent.Fast})
// Check err and select a returned text. Record an authorized speaker reading it.
d, err := c.Detect(ctx, tencent.DetectionRequest{
    TextID: textID, Audio: recording, Type: tencent.SoundQuality,
    Format: tencent.WAV, SampleRate: 24000, TaskType: tencent.Fast,
})
// Check err, d.Passed(), and d.AudioID before creating a task.
r, err := c.Clone(ctx, tencent.CloneRequest{
    SessionID: sessionID, VoiceName: "character", VoiceGender: 1, VoiceLanguage: 1,
    AudioIDs: []string{d.AudioID}, TaskType: tencent.Fast, EngineType: "YTLX_V2",
})
// QueryTask(ctx, r.TaskID) returns asynchronous training status.
// Query(ctx, VoiceRef{...}) reports deployment and expiry.
```

For one-sentence cloning the training text ID expires after seven days or successful task creation. Supply authorized audio longer than five seconds and shorter than fifteen seconds, at most 2 MB, mono 16-bit; 24/48 kHz WAV is recommended. The SDK does not decode or inspect media. Quality failure is a provider result, not an HTTP error; inspect Code, Message and Tips. Only successful quality detection returns an AudioID. Fast clone takes one ID and does not support callbacks. Basic clone accepts multiple IDs, optional callback and VPR audio. Training success is distinct from deployment; use Query and check expiry. Pass the returned VoiceType (as a decimal string) and FastVoiceType via tts/providers/tencent Options to synthesize. No automatic polling, retry, callback server, recording, or resource management occurs. Cancellation and offline model download are not public operations; the offline model type can be requested but download is not implemented. No public design or delete operation is implemented.

See https://cloud.tencent.com/document/api/1283/90075 and https://cloud.tencent.com/document/api/1283/90065 for the API and result definitions.

The read-only live workflow verifies training text and voice list retrieval. Detail and task queries require existing account resources. Audio quality detection and task creation require a suitable authorized recording; the workflow never uploads audio or creates a training task.
