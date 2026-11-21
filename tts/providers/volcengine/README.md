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
    Voice: "zh_female_shuangkuaisisi_moon_bigtts",
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

Supported provider formats: PCM16LE, MP3 or Ogg Opus (mono at the V3-supported
sample rates). The WAV option is intentionally omitted: the provider can emit
repeated WAV headers during streaming, which is not a valid concatenated
single WAV file. No decoding, repackaging or transcoding is performed.

The SDK serializes a V3 JSON text request, validates response frame lengths
and optional gzip payloads, returns event 352 audio bytes, and ends only on
event 152 with `status_code=20000000`. Errors retain the remote code.
Mock tests verify request headers, protocol framing, consecutive audio,
stable EOF, cancellation, and malformed responses.

**Live-service status:** Offline protocol tests do not establish that a
Volcengine TTS resource is activated or successfully synthesizes using a
particular API Key/voice. Real-cloud TTS integration must be enabled and
verified separately; existing Volcengine ASR checks are not TTS validation.
