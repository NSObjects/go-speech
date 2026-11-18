# Aliyun CosyVoice TTS Provider

This package implements **both** `tts.StreamSynthesizer` and
`tts.Synthesizer` against the official Alibaba Cloud Model Studio
CosyVoice WebSocket task protocol.

- Model defaults to `cosyvoice-v3-flash`. Choose a supported model/voice
  combination in your region; `longanyang` is a V3 Flash system voice.
- Configure the **matching regional workspace endpoint** and API key.
  For example, the official endpoint is
  `wss://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference`
  (Beijing), or use the Singapore workspace-specific host.
- `SynthesizeStream` sends `run-task`, waits for `task-started`, sends
  one `continue-task` with complete text, then **always** sends `finish-task`.
  `Recv` returns the raw binary audio frames and reaches stable `io.EOF`
  on `task-finished`.
- `Synthesize` uses the same stream and collects into one `audio.Buffer`
  (64 MiB in-memory limit).
- Current verified code paths accept PCM16LE, MP3, WAV, mono at
  8/16/22.05/24/44.1/48 kHz. Default output is MP3 22050 Hz mono.
  `opus` is intentionally excluded until the output container has its own
  cloud integration tests. No transcoding is performed.
- A fresh WebSocket is created per synthesis. Multi-turn persistent
  connection reuse and incremental text input are **not** implemented.
- Audio does not go to the user's speaker and is not tied to devices or agents.

```go
client, err := aliyun.New(aliyun.Config{
    APIKey: os.Getenv("DASHSCOPE_API_KEY"),
    Endpoint: os.Getenv("DASHSCOPE_WS_ENDPOINT"),
    Model: aliyun.ModelCosyVoiceV3Flash,
})
if err != nil { return err }

req := tts.Request{
    Text: "你好，今天过得怎么样？",
    Voice: "longanyang",
    Format: audio.Format{
        Encoding: audio.EncodingPCM16LE, SampleRate: 22050, Channels: 1,
    },
}
stream, err := client.SynthesizeStream(ctx, req)
if err != nil { return err }
defer stream.Close()
for {
    chunk, err := stream.Recv(ctx)
    if errors.Is(err, io.EOF) { break }
    if err != nil { return err }
    // Forward chunk.Data unchanged; chunk.Format describes actual bytes.
    _ = chunk
}
```

**Live cloud test**: The automatic paid `Aliyun CosyVoice TTS Integration`
workflow runs on each push using existing `DASHSCOPE_API_KEY` and
`DASHSCOPE_WS_ENDPOINT`, optionally `DASHSCOPE_TTS_MODEL` and
`DASHSCOPE_TTS_VOICE` repository variables. It verifies a non-empty PCM
stream, format consistency, and stable task termination; it does **not**
verify subjective speech quality or production latency.

Official documentation:
- https://help.aliyun.com/en/model-studio/cosyvoice-websocket-api
- https://help.aliyun.com/en/model-studio/cosyvoice-client-events
- https://help.aliyun.com/en/model-studio/cosyvoice-server-events
