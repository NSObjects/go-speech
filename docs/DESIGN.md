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

## Next milestone

Implement one ASR provider and one TTS provider behind these contracts with
contract tests, then compare a second vendor before finalizing the public API.

## Errors

Provider adapters wrap remote failures in `speecherr.Error` with a stable
classification (`Code`) while preserving the original `Err` for `errors.Is`.
Retryability is advisory; the application decides whether retrying is safe.
