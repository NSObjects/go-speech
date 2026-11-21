# Volcengine Seed TTS v3 — unidirectional WebSocket

Provider `tts/providers/volcengine` implements the official new-console
`wss://openspeech.bytedance.com/api/v3/tts/unidirectional/stream`
API. It accepts one complete text request and emits audio chunks through the
shared `tts.StreamSynthesizer` interface. `Synthesize` also collects a
complete result (capped at 64 MiB).

Official protocol: https://docs.volcengine.com/docs/doubao-voice/websocket-unidirectional-streaming-v3?lang=zh

```go
client, err := volcengine.New(volcengine.Config{
    APIKey: os.Getenv("VOLCENGINE_TTS_API_KEY"),
    ResourceID: volcengine.ResourceSeedTTS2, // seed-tts-2.0
})
if err != nil { return err }

stream, err := client.SynthesizeStream(ctx, tts.Request{
    Text: "你好，欢迎使用语音合成。",
    Voice: "zh_female_vv_uranus_bigtts",
    Format: audio.Format{
        Encoding: audio.EncodingMP3, SampleRate: 24000, Channels: 1,
    },
})
if err != nil { return err }
defer stream.Close()

for {
    chunk, err := stream.Recv(ctx)
    if errors.Is(err, io.EOF) { break }
    if err != nil { return err }
    consume(chunk)
}
```

The adapter authenticates with **X-Api-Key**, **X-Api-Resource-Id** and an
independent request ID. Old-console AppID/AccessToken authentication is
intentionally omitted. Resource entitlement and speaker choice must match:
`seed-tts-2.0` requires an activated 2.0 voice.

Supported provider formats: PCM16LE and MP3 (mono at 8, 16, 22.05, 24,
32, 44.1 or 48 kHz); Ogg Opus is **48 kHz mono only**. Other Ogg Opus
sample rates are rejected locally before the SDK opens a network session. The WAV option is intentionally omitted: the provider can emit
repeated WAV headers during streaming, which is not a valid concatenated
single WAV file. No decoding, repackaging or transcoding is performed.

The SDK serializes a V3 JSON text request, validates response frame lengths
and optional gzip payloads, returns event 352 audio bytes, and ends only on
event 152. Its payload may contain `usage` without `status_code`; any explicit non-success status is still an error. Errors retain the remote code.
Mock tests verify request headers, protocol framing, consecutive audio,
stable EOF, cancellation, and malformed responses.

**Live-service status:** The real Seed TTS 2.0 smoke test on PR #74
successfully synthesized 114,138 bytes of PCM24k audio in seven chunks using
`zh_female_vv_uranus_bigtts`, then reached stable EOF. This validates one
specific account/resource/voice combination; other resources and voices must
be checked separately. Existing Volcengine ASR checks are not TTS validation.
