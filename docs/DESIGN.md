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

- `speech.NewASR(speech.ASRConfig) (asr.StreamRecognizer, error)`
- `speech.NewTTS(speech.TTSConfig) (tts.Client, error)`

Supported config types are `speech.AliyunASR`, `speech.VolcengineASR`,
`speech.TencentASR` and their `TTS` counterparts. Config values and pointers
are accepted; nil or typed-nil configs return `ErrInvalidASRConfig` or
`ErrInvalidTTSConfig`. Invalid provider options return provider errors instead.

The constructors do not open network sessions. ASR connects via
`OpenStream`; TTS connects via `Synthesize` or `SynthesizeStream`.
Each request creates an independent vendor session. Callers own credentials,
configuration loading, model selection, and service-level retry policies.
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

`asr.Recognizer` also defines a separate one-shot `Recognize` interface,
but `speech.NewASR` returns **only** `asr.StreamRecognizer`. Do not
infer that all built-in providers implement batch recognition.

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
models, and formats. Real-cloud verification of Tencent TTS audio is
**not complete**: the test account returns resource-pack exhaustion (3022),
which is recorded as a warning rather than verified PCM synthesis.
Neither offline mocks nor green CI alone prove every voice/model combination.
