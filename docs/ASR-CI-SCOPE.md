# ASR CI acceptance scope

`go-speech` is a provider API SDK, **not** a speech-recognition
accuracy benchmark. Its routine Actions must validate wire compatibility and
SDK lifecycle without grading a model's pronunciation, vocabulary or CER.

## Automatic gates

| Scope | Required tests on every push |
|---|---|
| Go | Unit, protocol/parser, configuration, error and race tests; native codec tests in `Go CI` |
| Tencent | Cloud PCM handshake, Send/Recv/CloseSend, stable EOF, per-request hints, concurrency, 8 kHz input, large-model V2, speaker metadata (separate speaker model entitlement) |
| Aliyun | All five supported real-time ASR model tasks, streamed PCM, nonempty final event, and Fun-ASR Ogg Opus passthrough / packet remux |
| Volcengine | Both result types over real PCM, Ogg Opus upload, nonempty Final event and repeatable EOF |

The paid ASR workflows still run **automatically on push**. Their
`workflow_dispatch` options remain available for exceptional investigation
but are not required for normal acceptance.

The CI sample for Volcengine uses an integrity-checked, publicly available
PCM recording, plus externally constructed Ogg Opus, with no SDK transcoding.
The corresponding tests do **not** require the cloud model to produce any
specific characters. An empty Final, protocol error, failed upload or
unstable EOF is a failed API smoke test.

## Outside routine gates

Word-for-word matching, WER/CER, VAD threshold comparisons, natural-language
quality, and repeated 4/12-turn / minute-plus speech benchmarks are
**model or workload measurements**. The historical integration tests remain
in the repository for development diagnostics but do not execute on every
push. Removing them from the automatic workflow does not delete those tests
or weaken protocol/parser unit tests.

Do not hide vendor authentication, protocol incompatibility or SDK resource
leaks by classifying them as model quality. Do not add implicit codec
transcoding, transcript normalization, audio replay or retry to the SDK
merely to make cloud checks pass.

If a specific provider API adds a new transport format, event field,
request option or lifecycle behavior, add a deterministic local unit test and
a minimal automatic real-provider smoke test for that API feature.

The three Volcengine natural-Mandarin quality diagnostic workflows are
`workflow_dispatch` only; they are **not** the automatic paid ASR gate.
Aliyun TTS auto-CI is path-filtered to TTS/shared dependency changes and is
not triggered by an ASR-only code or workflow change.
