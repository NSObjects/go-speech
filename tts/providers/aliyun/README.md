# Aliyun CosyVoice TTS Provider

This package implements **both** `tts.StreamSynthesizer` and
`tts.Synthesizer` against the official Alibaba Cloud Model Studio
CosyVoice WebSocket task protocol.

- Model defaults to `cosyvoice-v3-flash`. Choose a supported model/voice
  combination in your region; `longanyang` is a V3 Flash system voice.
- The default endpoint is the China DashScope service. For another region or dedicated workspace, set `Endpoint` alongside the API key.
  For example, the official endpoint is
  `wss://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference`
  (Beijing), or use the Singapore workspace-specific host.
- `SynthesizeStream` sends `run-task`, waits for `task-started`, sends
  one `continue-task` with complete text, then **always** sends `finish-task`.
  `Recv` returns the raw binary audio frames and reaches stable `io.EOF`
  on `task-finished`.
- `Synthesize` uses the same stream and collects into one `audio.Buffer`
  (64 MiB in-memory limit).
- Supported formats are PCM16LE, MP3, WAV and **Ogg Opus**, mono at
  8/16/22.05/24/44.1/48 kHz. Default output is MP3 22050 Hz mono.
  `audio.EncodingOpus` means Ogg-encapsulated Opus, not raw Opus packets.
  The actual codec, framing and decoding for the 24 kHz Ogg Opus path
  are checked in the paid live cloud CI. No transcoding is performed.
- A fresh WebSocket is created per synthesis. Multi-turn persistent
  connection reuse and incremental text input are **not** implemented.
- Audio does not go to the user's speaker and is not tied to devices or agents.

```go
client, err := aliyun.New(aliyun.Config{
    APIKey: os.Getenv("DASHSCOPE_API_KEY"),
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

## Streaming encoded output verification

The automatic, billable integration test now checks **PCM, streaming MP3,
streaming WAV and batch MP3**. For encoded streams, the provider returns
successive slices of ONE media bitstream: a single `Recv` chunk is not
necessarily a standalone playable MP3/WAV file. Concatenate `Chunk.Data`
in receive order for a file or pass the stream to an incremental decoder.

The live CI test uses `ffprobe` on concatenated bytes to inspect the
**actual** codec, sample rate (22,050 Hz) and mono channel count rather than
trusting only `Chunk.Format`. WAV must start with RIFF/WAVE. No conversion
is performed by the SDK. Native mock tests also cover batch empty-output,
parent/per-call cancellation, idempotent close, task-ID mismatches, and
early task failure.

The live fixture validates only the selected V3 Flash model and voice in
one region at one sample rate. Do not infer from these tests that every
listed rate, voice or model has been verified.

## Ogg Opus output

To request compressed audio, set:
```go
req.Format = audio.Format{
    Encoding: audio.EncodingOpus, SampleRate: 24000, Channels: 1,
}
```
The provider requests `format: "opus"` from CosyVoice and forwards all
WebSocket binary audio bytes unchanged. The output is **Ogg Opus**, not
bare Opus packets. Concatenate `Chunk.Data` in order to obtain one complete
Ogg bitstream, or feed the successive chunks to an incremental Ogg decoder.
Do not assume a WebSocket chunk is a standalone Ogg page or a playable file.

Opus decoders use a 48 kHz decoding/granule timeline even if the synthesis
request was for 24 kHz. In the integration test `ffprobe` checks the actual
`opus` codec and mono channel count; `ffmpeg` checks that the complete
stream decodes correctly. Opus is not re-encoded or remuxed by the SDK.
