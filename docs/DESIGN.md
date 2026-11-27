# SDK design and public API contracts

This document describes the current `go-speech` contracts. The code in
`speech.go`, `speech_tts.go`, `asr/asr.go`, and `tts/tts.go` is authoritative.

## Scope

`go-speech` is a Go library for vendor-neutral ASR and TTS, not a hosted
speech service or an Agent runtime. The Go module has independent
`asr`, `tts`, `audio`, and `speecherr` packages.

The core adapters do not manage VAD policy, device/MQTT traffic, microphone
capture, audio playback, turn-taking, conversation memory, monitoring, or
application-level retries. Audio conversion is **explicit** through separate
`audio` utilities; provider adapters never silently convert supplied bytes.

## Provider selection

The root package exposes:

- `speech.NewASR(speech.ASRConfig) (asr.Client, error)`
- `speech.NewTTS(speech.TTSConfig) (tts.Client, error)`
- `speech.NewDuplexTTS(speech.TTSConfig) (tts.DuplexSynthesizer, error)`

Supported config types are `speech.AliyunASR`, `speech.VolcengineASR`,
`speech.TencentASR` and their `TTS` counterparts. Config values and pointers
are accepted; nil or typed-nil configs return `ErrInvalidASRConfig` or
`ErrInvalidTTSConfig`. Invalid provider options return provider errors instead.

Constructors do not open network sessions. ASR starts recognition via
`Recognize` or `OpenStream`; TTS starts synthesis via `Synthesize`,
`SynthesizeStream`, or `OpenDuplex`. Each request creates an independent
vendor task, which may lease an existing connection when pooling is enabled.
Callers own credentials, configuration loading, model selection, and
service-level retry policies.
The root configuration types intentionally select only built-in adapters;
for custom implementations, use the separate `asr` / `tts` interfaces.

## ASR contract

`asr.StreamRecognizer.OpenStream(ctx, asr.Config)` returns `asr.Stream`:

- `Send(ctx, data)` submits bytes in the declared audio encoding.
- `CloseSend(ctx)` finishes input without discarding pending recognition.
- `Recv(ctx)` yields `EventPartial` and `EventFinal`; optional
  `EventSpeechStart` / `EventSpeechEnd` depend on the vendor.
- `Close()` releases the session. Always close it even after EOF or failure.

One concurrent sender and one concurrent receiver are supported, but two
simultaneous senders or two simultaneous receivers are **not** guaranteed safe.
A Final segment is not necessarily a complete conversation turn.
`Event.SpeakerID` is optional, provider-specific, session-local metadata;
`nil` means absent, `-1` pending, and nonnegative values are labels,
not persistent identities.

`asr.Client` combines `Recognizer` and `StreamRecognizer`. All three
built-in providers implement `Recognize(ctx, audio.Buffer, asr.Config)`
for a complete audio buffer. This is a convenience over the provider's
streaming recognition path, not a separate offline recognition service.

Request-local `asr.Options` controls recognition features. With
`UtteranceDetails` enabled, final segments may include native timings,
word metadata, speaker labels, and other provider fields. Omitted
timestamps stay absent. `EventUtteranceUpdate` replaces metadata for a
previously emitted final segment without appending more transcript text.
Provider-specific session metadata may be retrieved using
`asr.SessionMetadataReceiver` after draining to EOF.

## TTS contract

`tts.Client` combines `Synthesize(ctx, tts.Request)` and
`SynthesizeStream(ctx, tts.Request)`:

- `Synthesize` collects a complete `audio.Buffer` in memory.
- `SynthesizeStream` yields `audio.Chunk` through `Recv(ctx)`.
- A `tts.Request` contains the **complete text**; streaming means audio
  output, **not** incremental text input.
- Each stream supports one `Recv` caller; `Close()` is idempotent.

The requested `Voice` identifier is vendor-specific. Zero-valued
`audio.Format` selects a provider default; an explicit unsupported
format is rejected. Compressed audio chunks are fragments of a complete
bitstream: concatenate in order or pass them to a streaming decoder.
A batch request has a 64 MiB output limit in the built-in adapters.
Batch collection rejects empty audio and format changes, and returns no
partial audio on failure. Use streaming for larger output.

### Incremental text and alignment

`tts.DuplexSynthesizer.OpenDuplex` accepts a `tts.DuplexRequest` and
returns a `tts.DuplexStream`. Send text fragments with `SendText`, finish
input with `CloseSend`, and concurrently drain audio. Serialize send and
control operations; only one receiver is supported.

`tts.EventReceiver.RecvEvent` exposes available alignment and batch
completion events in addition to audio. Use either `Recv` or `RecvEvent`
for the entire session. `tts.SynthesizeDetailed` collects one complete
text request with audio and bounded alignment metadata. Explicitly enable
provider timestamps when needed; absent timings are never fabricated.

Native controls are optional interfaces: Seed-TTS `Flusher`, Tencent
`Resetter`, and Aliyun `Canceler`. A successful control write is not a
terminal acknowledgement. A flushed batch can be a separate encoded
bitstream. See the provider contracts for text formats and control semantics.

## Connection pooling

Pooling is opt-in through `Pool: &speech.PoolConfig{...}`. Aliyun ASR/TTS
reuse eligible completed WebSocket tasks; Seed-TTS only pools duplex
connections, keyed by voice-specific endpoint. Default clients do not pool.
Each active task exclusively owns a leased socket. Failure or early close
discards the socket; there is no automatic task replay or idle prefill.

Clients with pooling implement `io.Closer`; close the client at shutdown
and close each stream after use. Pool limits apply per client. See
[connection management and provider support](connection-management.md). Aliyun and Seed-TTS TTS clients expose optional explicit `tts.Prewarmer`; Tencent TTS instead supports opening the actual duplex session before text is available. Tencent ASR/TTS and Volcengine bigmodel_async ASR do not reuse completed sockets.

## Voice management and local capabilities

The `voice/providers/{aliyun,volcengine,tencent}` packages expose separate
provider-native management clients for voice resources. Voice IDs and
readiness states are provider-specific; synthesis clients do not implicitly
create or deploy voices.

Optional capability and validator interfaces expose local model/protocol
support and request validation. They do not probe account entitlements or
remote voice support. Provider-specific options retain native semantics.

## Cancellation, errors, and retry policy

Both streaming APIs reserve repeated `io.EOF` for **successful completion**.
After a terminal error, subsequent `Recv` calls preserve the terminal cause;
cancellation returns `context.Canceled` / `context.DeadlineExceeded`.
Closing a stream prematurely returns `tts.ErrStreamClosed` for TTS;
ASR distinguishes `asr.ErrStreamClosed` from `asr.ErrInputClosed`
after a successful half-close.

Provider errors use `speecherr.Error`, including `Code`, `RemoteCode`,
`Retryable`, and wrapped `Err`. Use `speecherr.IsCode` for categories and
`errors.Is` / `errors.As` for original causes. `Retryable` is advisory:
the SDK does not automatically replay audio or duplicate billable requests.
A broken WebSocket session cannot safely be resumed.

## Audio boundaries

`audio.Format` must describe the actual bytes; `audio.EncodingOpus`
means **Ogg Opus**, not raw device Opus packets. The SDK exposes explicit
PCM/WAV conversion, pure-Go Ogg encapsulation, and optional FFmpeg/native
codec adapters in separate packages. None is implicitly invoked by
`asr.Stream.Send` or a TTS provider.

See [audio handling](AUDIO.md) and [integration testing](INTEGRATION.md).

## Verification scope

Offline Go CI runs format checks, vet, race tests, and optional native
codec builds. Paid vendor workflows validate selected real ASR/TTS protocols,
models, and formats. The Tencent TTS integration workflow distinguishes resource-pack exhaustion
(3022) from verified audio synthesis. Read-only voice management checks
report missing existing resources as NOT VALIDATED rather than verifying
detail queries or training mutations. Consult the exact revision's job logs.
Neither offline mocks nor green CI alone prove every voice/model combination.
