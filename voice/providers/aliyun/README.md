# Aliyun voice management

The DashScope voice-enrollment API supports CosyVoice and Qwen Audio voice resources independently of the TTS client.

```go
client, err := aliyun.New(aliyun.Config{APIKey: apiKey})
if err != nil {
    return err
}
r, err := client.Clone(ctx, aliyun.CloneRequest{
    TargetModel: "cosyvoice-v3-flash",
    Prefix: "character",
    Audio: aliyun.AudioSource{Data: recording, Format: aliyun.WAV},
})
if err != nil {
    return err
}
// r.VoiceID identifies the created voice; creation alone does not confirm review.
r, err = client.Query(ctx, r.VoiceID)
```

Use the returned VoiceID with the same target model when synthesizing. Query reports DEPLOYING, OK or UNDEPLOYED; new or unknown status is not treated as ready. List returns one page of custom voices; the caller chooses pagination. Update replaces a clone's reference recording without changing its ID; updating designed voices is not documented. Delete is explicit. Design accepts text description and preview text and returns decoded preview audio bytes. Design may return a created ID together with a preview protocol error; do not blindly resubmit.

The default Beijing DashScope origin remains supported. Set Endpoint to the appropriate workspace-specific or regional HTTPS origin. Model/region availability and permissions are determined by the provider. Qwen enrollment support does not imply a Qwen synthesis adapter is implemented in go-speech.

AudioSource accepts HTTP(S) URLs or WAV/MP3/M4A bytes (up to 10 MB); the current audio field is used instead of the deprecated url field. The caller must provide authorized recordings meeting the provider's media requirements. The SDK does not inspect, fetch or transform samples, poll review status, store voice resources, or automatically retry mutations. All operations use one HTTP request, configurable timeout and a bounded response.

References:
- https://help.aliyun.com/zh/model-studio/voice-clone-design-http-api
- https://help.aliyun.com/zh/model-studio/voice-design-api-references
- https://help.aliyun.com/zh/model-studio/voice-cloning-user-guide
