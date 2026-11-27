# Tencent Cloud TTS

The default `New` client implements Tencent Cloud **TextToStreamAudioWSv2**
with complete-text and incremental-text synthesis. It returns the provider's
PCM16LE or MP3 bytes incrementally. Use the same client for one-shot,
streaming-output and duplex requests. Select `NewHTTP` explicitly only when
native complete-text HTTP features such as SSML or WAV are needed.
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
The signed URL binds the session ID and synthesis settings. FINAL terminates that session and the protocol requires closing the WebSocket. This provider does not implement connection pooling or `tts.Prewarmer`. To overlap setup with other work, open the duplex session with the actual request options before text is ready; keep its context valid through synthesis and close it if unused. A real-cloud test successfully synthesized after waiting five seconds following READY; this is not a guaranteed idle timeout.

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

## Native complete-text HTTP synthesis

WSv2 does not support SSML or WAV output. For these native features, use `NewHTTP(HTTPConfig{SecretID: id, SecretKey: key})` for the Cloud API
`TextToVoice` (2019-08-23). It implements `tts.Synthesizer`, not streaming
or duplex; register it as `tts.Provider.Batch` independently of WSv2.
The default is mono 16 kHz WAV. PCM and MP3 are also native options. The
existing WebSocket client is preferred for ordinary text and PCM/MP3; there
is no automatic fallback between protocols.
Use `tts.TextSSML` to pass supported markup unchanged; voice-specific
restrictions and text-length limits apply (150 Chinese characters or 500 English letters per official protocol; the service decides mixed text and SSML limits). No fallback or local text splitting. Cloud error codes are retained, including entitlement, resource-pack exhaustion and request limits.

```go
c, err := tencent.NewHTTP(tencent.HTTPConfig{
    SecretID: id, SecretKey: key,
})
if err != nil { return err }
out, err := c.Synthesize(ctx, tts.Request{
    Voice: "101001",
    Text:  `<speak>你好<break time="500ms"/>世界</speak>`,
    TextFormat: tts.TextSSML,
})
```

Use `HTTPOptions` to select native `ProjectID`, `ModelType` (1),
`PrimaryLanguage` (1 Chinese, 2 English), and the shared `Options`.
For subtitles, enable `EnableSubtitle` and use `c.SynthesizeDetailed`;
it returns native nullable phonemes, indices, request/session IDs and common
alignment data. No markup translation or audio conversion is performed.

Official protocol: https://cloud.tencent.com/document/api/1073/37995
SSML: https://cloud.tencent.com/document/product/1073/49575
Voice restrictions: https://cloud.tencent.com/document/product/1073/92668
