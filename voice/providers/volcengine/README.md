# Voice management: Seed-TTS 3.0

Use a separate client for enrollment, query and design.

```go
import "github.com/NSObjects/go-speech/voice/providers/volcengine"

client, err := volcengine.New(volcengine.Config{APIKey: apiKey})
if err != nil {
    return err
}
result, err := client.Clone(ctx, volcengine.CloneRequest{
    Voice:  volcengine.VoiceRef{SpeakerID: speakerID},
    Audio:  recording,
    Format: volcengine.WAV,
})
if err != nil {
    return err
}
if result.State().Usable() {
    // Pass result.SpeakerID to tts.Request.Voice when synthesis is needed.
}
```

Use an existing purchased voice slot, or set VoiceRef.CustomSpeakerID for postpaid enrollment. Query accepts either reference. Design requires a prepaid S_ slot and a text prompt and/or image prompt. Preview URLs are returned as provided and may expire after one hour. The client does not download them. A usable state does not guarantee model compatibility or account entitlement.

Only supply recordings and images you are authorized to use. Audio should match the documented 10–60 second duration; PCM must be 24 kHz mono. The adapter checks size, declared format, and request structure, not the media encoding, duration, source language or account availability. Use the provider's language table for language-dependent clone options. A custom voice may incur charges when activated by synthesis. No automatic retry, activation, polling, slot purchase, or deletion occurs. Timeout is configurable; the default is 120 seconds. A timeout does not prove a mutation failed; query the slot before deciding to resubmit.

Official references:
- https://docs.volcengine.com/docs/doubao-voice/seed-tts-3-0-voice-cloning-register?lang=zh
- https://docs.volcengine.com/docs/doubao-voice/seed-tts-3-0-voice-query?lang=zh
- https://docs.volcengine.com/docs/doubao-voice/seed-tts-3-0-voice-design-api?lang=zh
