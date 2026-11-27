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
The signed URL binds the session ID and synthesis settings. FINAL terminates that session and the protocol requires closing the WebSocket. This provider does not implement connection pooling or `tts.Prewarmer`. To overlap setup with other work, open the duplex session with the actual request options before text is ready; keep its context valid through synthesis and close it if unused.

Only `pcm` and `mp3` at 8/16/24 kHz mono are accepted; individual
voices may support fewer sample rates.

Use `OpenDuplex` for incremental text input. The session accepts at most
10,000 Unicode characters in total, with text sent via `SendText`. Complete
input with `CloseSend` and drain audio until EOF. The SDK counts successfully
submitted text; RESET does not restore the local budget because the service
does not document how discarded text affects the limit.

Local mock tests verify signing, commands, streaming, cancellation, errors
and terminal EOF. A genuine Tencent TTS API smoke test requires an
activated TTS product and account entitlement: ASR credentials alone do not
prove that Tencent TTS is enabled. No successful live Tencent TTS synthesis
is claimed from the ASR CI workflow.

## Native advanced request options

Set `tts.Options.Provider` to `tencent.Options` to select `FastVoiceType` with `Request.Voice` (or `DuplexRequest.Voice`) equal to 200000000 or 200000400 for one-sentence cloning. `EnableLatex` is optional; it is only supported by certain Chinese super-natural 2.0 voices and may increase latency. Both parameters are included in the signed query. Set `EnableSubtitle` to true and use `tts.EventReceiver.RecvEvent` to receive word timestamps and phonemes.

Use one receive API per session: `Recv` for audio only, or `RecvEvent` for audio and alignment. Explicit timestamp enablement is rejected by audio-only `Synthesize`; use `tts.SynthesizeDetailed(ctx, client, req)` for a complete result or `SynthesizeStream` with `RecvEvent`. Provider model/voice restrictions apply. A subtitle may have empty text and native phoneme `SIL` (silence); the SDK preserves its timing and optional indices. Count only nonempty text entries when computing spoken-word statistics.

## Native reset

Duplex streams implement `tts.Resetter`. Reset sends ACTION_RESET without ending input, clearing server-side pending text only. Already-started synthesis is unaffected. SendText or CloseSend may follow Reset. A successful write is not a server acknowledgment; the SDK neither drops already-received audio nor simulates a cancellation.
