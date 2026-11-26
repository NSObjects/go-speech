# Tencent Cloud TTS (WebSocket stream_wsv2)

This provider implements Tencent Cloud **TextToStreamAudioWSv2** with
`tts.Synthesizer` and `tts.StreamSynthesizer`. It accepts one **complete**
text request and returns the provider's PCM16LE or MP3 bytes incrementally.
The provider does not resample, transcode, cache or play audio.

Official protocol: https://cloud.tencent.com/document/api/1073/108595

## Usage

```go
client, err := tencent.New(tencent.Config{
    AppID:     appID,
    SecretID:  secretID,
    SecretKey: secretKey,
})
if err != nil { return err }

stream, err := client.SynthesizeStream(ctx, tts.Request{
    Text:  "你好，欢迎使用语音合成。",
    Voice: "101001", // Tencent numeric VoiceType; must be available to the account
    Format: audio.Format{
        Encoding: audio.EncodingPCM16LE,
        SampleRate: 16000,
        Channels: 1,
    },
})
if err != nil { return err }
defer stream.Close()

for {
    chunk, err := stream.Recv(ctx)
    if errors.Is(err, io.EOF) { break }
    if err != nil { return err }
    // Deliver chunk.Data to a streaming decoder/player. Chunks are consecutive
    // portions of one audio stream, not independent files.
    consume(chunk)
}
```

`Synthesize(ctx, req)` collects a complete result (maximum 64 MiB).
The SDK signs a new WebSocket URL/session per synthesis with HMAC-SHA1,
waits for Tencent's `ready=1`, sends `ACTION_SYNTHESIS` and
`ACTION_COMPLETE`, and drains audio until `final=1`.
Only `pcm` and `mp3` at 8/16/24 kHz mono are accepted; individual
voices may support fewer sample rates.

This adapter does **not** implement incremental text ingestion from the
caller. Tencent's WebSocket v2 offers that separately, but the current
portable TTS interface accepts one complete `Request.Text`.

Local mock tests verify signing, commands, streaming, cancellation, errors
and terminal EOF. A genuine Tencent TTS API smoke test requires an
activated TTS product and account entitlement: ASR credentials alone do not
prove that Tencent TTS is enabled. No successful live Tencent TTS synthesis
is claimed from the ASR CI workflow.

## Native advanced request options

Set `tts.Options.Provider` to `tencent.Options` to select `FastVoiceType` with `Request.Voice` (or `DuplexRequest.Voice`) equal to 200000000 or 200000400 for one-sentence cloning. `EnableLatex` is optional; it is only supported by certain Chinese super-natural 2.0 voices and may increase latency. Both parameters are included in the signed query. Timestamp enablement awaits the SDK event interface.
