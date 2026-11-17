# Design notes (v0.1 foundation)

## Scope

This repository provides Go contracts and vendor adapters for speech recognition
(ASR) and text-to-speech (TTS). It is a library, not an agent framework, a
conversation orchestrator, an audio player, or a hosted service.

## API design

- One repository and one Go module; independent `asr`, `tts`, and `audio` packages.
- Implementations can provide batch, streaming, or both capabilities.
- Registries are application-owned; there is no global mutable provider list.
- Configuration shared across vendors stays small. Provider-specific credentials,
  model IDs, voice parameters, and transport settings live in provider constructors.
- `context.Context` is required on network operations; streams have explicit Close.
- ASR final transcript segments are not necessarily utterance boundaries.
- TTS streaming currently takes complete input text. Incremental text-to-audio
  sessions, if supported by vendors, will be an additional optional capability.
- No automatic cross-vendor failover. Mid-stream switching can require replaying
  audio and is the responsibility of the application.

## Stream contract

- ASR allows one concurrent sender and one receiver. `CloseSend` ends input but
  allows pending recognition results to drain via `Recv` until `io.EOF`.
- TTS `Recv` returns audio chunks until `io.EOF`.
- Implementations must honor cancellation, release sockets on `Close`, and
  make `Close` idempotent. Avoid retaining input buffers without documenting it.
- Streaming tests for each vendor must cover cancellation, EOF, and failures.

## Out of scope

VAD, endpointing policy, device/MQTT/WebSocket transport, playback state, audio
buffer arbitration, Agent Runtime, memory, codec transcoding, and speech turn
management belong to the application or other dedicated modules.

## Errors and streaming session lifecycle

Provider adapters wrap remote failures in `speecherr.Error` with a stable
classification (`Code`) while preserving the original `Err` for `errors.Is`.
`RemoteCode` carries the provider's original diagnostic code when supplied,
without requiring callers to parse error messages.

- `speecherr.CodeProtocol`: malformed or internally inconsistent remote
  notifications. These are **not** normally retryable.
- `asr.ErrStreamClosed`: stream closed or aborted by application; **not** a
  transient network failure. `asr.ErrInputClosed`: audio input has already
  been finished with `CloseSend`, but the result stream may still be drained.
- `context.Canceled` and `context.DeadlineExceeded` are preserved as-is.
  They are not recategorized as retryable network errors.
- After receiving `task-finished`, subsequent `Recv` calls return `io.EOF`.
- After a terminal failure, `Recv` preserves the first protocol/provider/
  transport error on subsequent calls so upper layers do not lose the cause.
- Failed writes close the WebSocket session: attempting to continue sending
  on a failed/possibly corrupt connection is unsafe.

`speecherr.Error.Retryable` is only advisory. Retrying a **streaming** request
is not the same as retrying a stateless API call: applications must decide
whether audio frames can be safely replayed in a fresh session, whether
billing/duplicates matter, and whether to expose partial transcription.

Native unit tests inject remote failure, malformed WebSocket notifications,
task-ID mismatches, unexpected disconnects, explicit Close, parent/per-call
cancellation, and repeated EOF/error reads. The paid automatic Aliyun ASR CI
checks actual model behavior separately.

## Next milestone

The Volcengine Doubao V3 optimized streaming adapter is now the second ASR
provider implementation. Both implement the same `asr.StreamRecognizer`
contract, but have separate WebSocket framing and credentials. Continue by
running live cloud integration and adding cross-provider contract tests.
Do not introduce implicit audio conversion into `Stream.Send`.
