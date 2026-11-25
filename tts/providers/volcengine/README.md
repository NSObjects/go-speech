# Volcengine Seed-TTS 3.0

Supports complete-text HTTP JSONL streaming and incremental-text WebSocket synthesis. Both use the same SDK audio and stream contracts. Configure the API key and an authorized Seed-TTS 3.0 voice ID; this adapter never registers voices or selects a preset automatically.

```go
client, err := speech.NewTTS(speech.VolcengineTTS{
    APIKey: apiKey,
})
```

Use `Synthesize`, `SynthesizeStream`, or `speech.NewDuplexTTS`. Set the voice on the request. MP3 and PCM support 8–48 kHz at the documented rates; Ogg Opus requires 48 kHz mono. No transcoding.

The duplex stream accepts fragments through SendText. An optional `Flush(context.Context) error` method submits a batch without closing input. CloseSend submits remaining text and Recv returns EOF only after the provider's CLOSED final event. Close cancels the network session.

Official references: [HTTP streaming](https://docs.volcengine.com/docs/doubao-voice/seed-tts-3-0-http-streaming?lang=zh), [WebSocket](https://docs.volcengine.com/docs/doubao-voice/seed-tts-3-0-websocket?lang=zh).

Migration: the previous ResourceID configuration field is removed; use optional ModelID (defaults to doubao-seed-tts-3.0). Endpoint is now an HTTPS origin. Existing 2.0 voice IDs must be replaced with authorized 3.0 voice IDs.
