# Tencent Cloud real-time ASR (WebSocket V2)

This provider implements `asr.StreamRecognizer` and `asr.Stream` with
Tencent Cloud Speech Recognition V2 (`/asr/v2/<appid>`). It is **not**
Tencent RTC's separate `/asr/v3` product or Tencent's TTS service.

## Authentication

Create and activate Tencent Cloud ASR service credentials. Configure:
- `TENCENT_APP_ID`: numeric Tencent account AppID.
- `TENCENT_SECRET_ID`: Tencent SecretId.
- `TENCENT_SECRET_KEY`: Tencent SecretKey.

The SDK generates a **new** `voice_id` and nonce per stream, creates a
sorted-query HMAC-SHA1 signature, and sends it only in the WSS handshake.
Never log signed WebSocket URLs: they carry authentication material.
The default engine is `16k_zh`, selected by `Config.EngineModel`.
Tencent's large-model 2.0 engines require the appropriate billing entitlement.

## Audio compatibility and lifecycle

The first implementation accepts only mono PCM16LE at the engine's rate:
16 kHz for `16k_...` engines, 8 kHz for `8k_...`. The caller controls
approximately real-time audio pacing (Tencent recommends 200 ms chunks per
200 ms). No resampling, codec conversion, VAD engine, device integration
or background goroutine sending is added by the SDK.

**Important**: Tencent `voice_format=10` is a proprietary length-prefixed
Opus frame format, not Ogg Opus. The shared `audio.EncodingOpus` refers to
**Ogg Opus**, so it is deliberately rejected by this provider. Do not
send Ogg Opus pages while claiming they are Tencent raw Opus frames.

`CloseSend` sends `{"type":"end"}` without closing the WebSocket.
`Recv` converts the traditional `result.slice_type=1/2` messages to
`EventPartial/EventFinal`. For Tencent Cloud 2.0 models ending in `_2.0`,
the SDK signs `result_mod=1` and also parses `sentences.sentence_list`
(`sentence_type=0/1`), returning all events from each message without
dropping results or duplicating confirmed sentence IDs. The shared Event
currently carries text, not speaker IDs. A completed `final=1` produces
stable EOF *after* any queued final sentences have been delivered.
The caller may send and receive concurrently. Explicit Close and context
cancellation terminate the session; terminal errors remain stable.

Per-request language and hints are currently unsupported rather than
silently ignored: choose the engine in `Config`. The SDK does not
implement non-streaming batch recognition via this adapter.

## Example

```go
client, err := tencent.New(tencent.Config{
    AppID: os.Getenv("TENCENT_APP_ID"),
    SecretID: os.Getenv("TENCENT_SECRET_ID"),
    SecretKey: os.Getenv("TENCENT_SECRET_KEY"),
    EngineModel: "16k_zh",
    NeedVAD: true,
})
if err != nil { return err }
s, err := client.OpenStream(ctx, asr.Config{
    Format: audio.Format{
        Encoding: audio.EncodingPCM16LE,
        SampleRate: 16000, Channels: 1,
    },
})
if err != nil { return err }
defer s.Close()
// One goroutine sends suitably paced audio and calls CloseSend.
// The other concurrently consumes Partial/Final until io.EOF.
```

## Tests

Offline mock WebSocket tests validate fresh voice IDs, deterministic HMAC
signature rules, early authentication failures, actual binary/text frames,
Partial/Final conversion, duplicate segment suppression, stable EOF and
the shared `asr.Stream` cancellation/half-close contract.

Automatic billable Tencent cloud CI runs on every push. It **requires**
the Tencent credentials above and uses the open-source `espeak-ng` and
`ffmpeg` tools to generate a small Mandarin PCM test fixture. Until the
secrets are configured and the workflow finishes green, real Tencent
compatibility must be treated as **unverified**.

Official protocol: https://cloud.tencent.com/document/product/1093/48982
