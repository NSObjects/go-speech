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

The provider accepts mono PCM16LE at the engine's native rate:
16 kHz for `16k_...` engines, 8 kHz for `8k_...`. For explicit 8 kHz
PCM input to a 16 kHz engine, set `Config.InputSampleRate = 8000` and pass
`audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 8000, Channels: 1}`.
The signed URL includes Tencent's `input_sample_rate=8000`; Tencent handles
upsampling. **The SDK does not resample or convert the bytes.**

## Optional Tencent-side sentence segmentation

On supported legacy real-time engines, configure `VADSilenceTime` and
`MaxSpeakTime` as `time.Duration` values. These change **Tencent's server-side
sentence boundaries**, not PCM capture, SDK timing, or local VAD:

```go
client, err := tencent.New(tencent.Config{
    AppID: appID, SecretID: secretID, SecretKey: secretKey,
    EngineModel: "16k_zh",
    NeedVAD: true,
    VADSilenceTime: 700 * time.Millisecond, // 500–2000ms; Tencent default 1000ms.
    MaxSpeakTime: 10 * time.Second,         // 5–90s; Tencent default 60s.
})
```

Both fields default to zero, which **omits** the query parameters and
preserves Tencent's default behavior. `VADSilenceTime` requires `NeedVAD=true`
and is available for `8k_zh`, `8k_zh_finance`, `16k_zh`, `16k_zh_en`,
`8k_zh_large`, `16k_en`, and `16k_en_large`. `MaxSpeakTime` is available
for `8k_zh`, `16k_zh`, `16k_zh_en`, and `8k_zh_large`. Both are
intentionally rejected for `*_2.0` engines because their separate published
protocol does not document these knobs. Whole-millisecond values outside
the vendor ranges return `CodeInvalidArgument` **before** connecting.
Tuning sentence boundaries may reduce recognition accuracy; benchmark against
the defaults for each application.

Official legacy protocol: https://cloud.tencent.com/document/product/1093/48982

`Config.HotwordID` optionally selects a Tencent-managed hotword table.
Leave it empty to use the account default. Alternatively, callers can supply
`asr.Config.Hints` for one recognition session: the SDK validates up to 128
hotwords and signs Tencent's temporary `hotword_list` parameter, assigning
moderate weight `5` to each item (e.g. `悟空|5,游戏|5`). Repeated hints are
deduplicated in their original order. Tencent limits each hint to 30 characters
(including no more than 10 Han characters), without whitespace, commas or
pipe delimiters. Invalid hints return `speecherr.CodeInvalidArgument`
**before** connecting.

`HotwordID` and request `Hints` are intentionally mutually exclusive:
Tencent would otherwise silently prefer the temporary list. Hints are
per-stream; the client is not mutated and concurrent sessions stay isolated.
No accuracy improvement is promised without comparing recognition results.
The caller controls approximately real-time audio pacing (Tencent recommends
200 ms chunks per 200 ms for the general realtime protocol and 40 ms chunks
for the separate V2 sentence protocol). No device integration, SDK-managed
VAD, or background audio-sending goroutine is added.

**Important**: Tencent `voice_format=10` is a proprietary length-prefixed
Opus frame format, not Ogg Opus. The shared `audio.EncodingOpus` refers to
**Ogg Opus**, so it is deliberately rejected by this provider. Do not
send Ogg Opus pages while claiming they are Tencent raw Opus frames.

`CloseSend` sends `{"type":"end"}` without closing the WebSocket.
`Recv` converts the traditional `result.slice_type=1/2` messages to
`EventPartial/EventFinal`. For Tencent Cloud 2.0 models ending in `_2.0`,
the SDK uses the V2 `engine_model_type` parameter and parses
`sentences.sentence_list` (`sentence_type=0/1`). It deliberately does
**not** send the undocumented `result_mod` option; Tencent previously
rejected `result_mod=1` with error 4001. All events are returned without
dropping results or duplicating confirmed sentence IDs. The shared Event
currently carries text, not speaker IDs. A completed `final=1` produces
stable EOF *after* any queued final sentences have been delivered.
A late provisional sentence update for an already finalized sentence ID
is discarded, preventing downstream callers from reverting committed text.
The caller may send and receive concurrently. Explicit Close and context
cancellation terminate the session; terminal errors remain stable, including
when `Send` or `CloseSend` fails during a WebSocket write. Subsequent `Recv`
returns the **original** terminal failure rather than a cleanup-induced
`ErrStreamClosed`.

An optional `Config.ReadIdleTimeout` limits how long a single `Recv` blocks
without a server message. It defaults to zero (**disabled**) for backward
compatibility; set it explicitly if the caller may use `context.Background()`
and needs protection against a half-open peer. An idle timeout returns a
retryable `speecherr.CodeTimeout` and terminates the session. A caller context
deadline takes precedence if it expires first. This is a socket-read watchdog,
not a VAD silence threshold and not an automatic reconnect mechanism.

Per-request `Language` is unsupported rather than silently ignored:
choose the engine in `Config`. Per-request `Hints` use Tencent temporary
hotwords, as documented above. The SDK does not implement non-streaming batch
recognition via this adapter.

## Reconnection and duplicate-result safety

A Tencent WebSocket session cannot be resumed. **After any disconnection,
including a retryable one, discard the old stream and call `OpenStream` again.**
This generates fresh `voice_id`, nonce and signature for the replacement.
The SDK deliberately does **not** replay previously sent audio, because a
successful local WebSocket write does not prove which PCM bytes Tencent
processed before the connection broke.

The caller owns the recording buffer and transcript lifecycle:
- Treat `EventPartial` as provisional UI text; commit `EventFinal` once.
- Preserve committed text across a reconnect, but do not automatically
  re-submit the corresponding PCM. Only send audio chosen explicitly for the
  **new** session.
- If replaying an unconfirmed audio tail is necessary, the application must
  choose the overlap boundary and deduplicate any resulting transcript. The
  SDK cannot guarantee exactly-once delivery across broken WebSockets.
- `speecherr.Error.Retryable` permits considering a new connection; it is
  **not** authorization to resend the original audio or billable request.
  Tencent remote error `4004` (quota exhausted) is reported as
  `CodeRateLimited` with `Retryable=false`. Error `4006` (concurrency
  limit) is transient and remains retryable; both retain their `RemoteCode`.

Tencent recommends pacing PCM input at approximately real-time speed.
The SDK leaves pacing and capture-buffer management with the caller.
Long sessions should keep VAD enabled so Tencent can delimit utterances.

## Example

```go
client, err := tencent.New(tencent.Config{
    AppID: os.Getenv("TENCENT_APP_ID"),
    SecretID: os.Getenv("TENCENT_SECRET_ID"),
    SecretKey: os.Getenv("TENCENT_SECRET_KEY"),
    EngineModel: "16k_zh",
    NeedVAD: true,
    ReadIdleTimeout: 30 * time.Second, // Optional: zero disables the watchdog.
})
if err != nil { return err }
s, err := client.OpenStream(ctx, asr.Config{
    Format: audio.Format{
        Encoding: audio.EncodingPCM16LE,
        SampleRate: 16000, Channels: 1,
    },
    Hints: []string{"悟空", "游戏"}, // Optional: session-specific Tencent hotwords.
})
if err != nil { return err }
defer s.Close()
// One goroutine sends suitably paced audio and calls CloseSend.
// The other concurrently consumes Partial/Final until io.EOF.
```

## Tests

Offline mock WebSocket tests validate fresh voice IDs, deterministic HMAC
signature rules, early authentication failures, actual binary/text frames,
Partial/Final conversion, duplicate segment suppression, stable EOF,
Send/CloseSend failure preservation, idle-read timeout, and the shared
`asr.Stream` cancellation/half-close contract.

Automatic billable Tencent cloud CI runs on every push using
`TENCENT_APP_ID` (GitHub Actions variable), `TENCENT_SECRET_ID` and
`TENCENT_SECRET_KEY` (secrets). It uses `espeak-ng` and `ffmpeg` to
create short Mandarin PCM fixtures. Cloud gates cover:
- a configured-engine smoke stream with finalized text and stable EOF;
- per-request temporary hotwords accepted by Tencent Cloud;
- three concurrent 16 kHz streams;
- a sustained ~20-second 16 kHz session;
- a minute-plus 16 kHz stream to check long-running PCM delivery and stable EOF;
- a real `16k_zh` session signed with 700ms VAD silence and 10s forced segmentation;
- 8 kHz PCM sent unchanged to a 16 kHz engine with `input_sample_rate=8000`.

Large-model 2.0 requires a separate billing entitlement. Set the optional
GitHub Actions variable `TENCENT_V2_ASR_MODEL` to `16k_zh_en_2.0` (or an
entitled `_2.0` engine) to enable its **real-cloud** CI gate. An unset
variable **skips** this gate and does not prove 2.0 interoperability.
A 2026-11-18 live probe of `16k_zh_en_2.0` returned Tencent error
`4001: result_mod=1 not support for appid ... engine:16k_zh_en_2.0`.
The SDK no longer sends that unsupported query parameter (see official
V2 WebSocket protocol: https://cloud.tencent.com/document/product/1093/131127).
This repairs a known request incompatibility, **not** proof of successful
2.0 cloud recognition or proof of account entitlement. The opt-in live
2.0 integration gate still requires `TENCENT_V2_ASR_MODEL`; the mock
WebSocket sentence-list tests run regardless of entitlement.

### VAD threshold comparison (synthetic diagnostic)

The automatic paid Tencent CI also tests **700ms**, **1000ms**, and **1500ms**
`VADSilenceTime` using three separate, sequential `16k_zh` sessions. Each
session receives *identical* espeak-ng PCM: one short speech fixture, a
deliberate 1200ms silent pause, the same speech fixture again, and a
2400ms trailing pause. The fixture trims synthesized trailing padding to
approximately locate the first speech end, without changing voiced audio.

GitHub Actions writes `VAD_COMPARE` log records to the workflow's job summary:
- `first_final_relative_to_fixture_end_ms`: wall-clock offset between the
  first emitted Final and the estimated end of the first synthetic phrase
  (may be negative if the engine finalized an earlier internal pause);
- `finals_before_second_speech`: count of final events delivered before
  the second phrase begins in the caller's approximately real-time pacing;
- `finals_total`, `partials`, and `stream_wall_ms`: session diagnostics.

These are **not** reliable estimates of natural-speech finalization latency,
premature segmentation rate, WER/CER, or missing utterances. Real conversation
evaluation requires multiple labeled recordings with speech-end timestamps,
transcript references, noise/overlap cases and repeated trials before choosing
a production VAD threshold. The comparison only asserts successful cloud
streaming, at least one nonempty Final and clean EOF for all three settings.

### Opt-in natural-recording evaluation (local only)

For a meaningful follow-up, record one or more naturally spoken, **mono
16kHz PCM16LE** clips and annotate both a whole-recording reference transcript
and speech-only start/end intervals. Keep the audio and labels on your own
machine; no files, transcripts, or speaker data are committed to GitHub.
Tencent Cloud ASR will still receive the PCM bytes during the test.

For example, create a local `vad-eval.json` next to a `conversation.pcm`:

```json
{
  "pcm_file": "conversation.pcm",
  "reference_text": "你好今天的天气怎么样",
  "speech_intervals": [
    {"start_ms": 200, "end_ms": 2600}
  ]
}
```

The reference is the **full** manually corrected transcript in chronological
order. Annotate speech onset/end in milliseconds relative to the first PCM
sample. Non-overlapping intervals must lie within the recording duration;
do not include silence as speech. Reference and audio contents are never
printed in CI logs. Set the usual Tencent credentials locally, then run:

```sh
TENCENT_VAD_EVAL_MANIFEST=/absolute/path/to/vad-eval.json \
  go test -tags=integration -run TestTencentLiveLabeledVAD \
    -count=1 -v -timeout 10m ./asr/providers/tencent
```

The command evaluates **700/1000/1500ms** in three fresh Tencent sessions
using identical, approximately real-time-paced audio. It reports
`VAD_LABELED` summaries: normalized character error rate (CER; ignoring
punctuation/spacing/case), Final/Partial counts, Finals delivered while
an annotated speech interval is underway, and last-Final wall-time
relative to the last annotated speech end. **No transcripts are logged.**

The timing is approximate: network latency and client scheduling affect
wall-clock arrival, and a Final occurring during an annotation may represent
an *earlier* phrase. It is not an exact premature-cut metric. A single
recording also cannot establish production performance; evaluate multiple
speakers, noisy conditions, long pauses and interruptions. If the manifest
is unset, this test explicitly **skips**; passing standard CI does not
demonstrate natural-speech accuracy. No automatic VAD default changes are made.

Do not equate synthesized test utterances with recognition-accuracy
benchmarks; these tests verify transport, lifecycle, and final-result
delivery.

Official protocol: https://cloud.tencent.com/document/product/1093/48982
