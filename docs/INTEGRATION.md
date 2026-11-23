# Integration testing

The repository distinguishes **offline SDK tests** from **paid real-cloud
tests**. A successful mocked protocol test does not demonstrate real provider
entitlement or audio quality. Consult the latest GitHub Actions result for
the exact revision and job, rather than treating this document as live status.

## Offline checks

The [Go CI workflow](../.github/workflows/ci.yml) runs for pull requests and
pushes to `main`. It runs Go formatting checks, `go vet`, `go test -race`,
and compilation of selected `integration`-tagged test files **without
executing those tests**. A separate job checks optional libopus and
OpenCORE-AMR-NB builds under their build tags.

```sh
go test ./...
go test -race ./...
go vet ./...
```

Offline mock WebSocket tests exercise framing, independent sessions,
cancellation, timeouts, terminal EOF/errors, and resource cleanup.
They do **not** validate paid provider services or account permissions.

## Paid provider workflows

| Workflow | Coverage | Trigger |
| --- | --- | --- |
| [Aliyun ASR](../.github/workflows/aliyun-asr-integration.yml) | Five configured ASR model variants; real streamed audio and final transcript | Every branch push and manual dispatch |
| [Volcengine ASR](../.github/workflows/volcengine-asr-integration.yml) | Selected live Doubao ASR paths and formats | See workflow triggers |
| [Tencent ASR](../.github/workflows/tencent-asr-integration.yml) | Realtime ASR and supported speaker engine | See workflow triggers |
| [Aliyun CosyVoice TTS](../.github/workflows/aliyun-tts-integration.yml) | Selected PCM/MP3/WAV/Ogg Opus streaming and batch cases | Relevant push paths and manual dispatch |
| [Volcengine + Tencent TTS](../.github/workflows/tts-live-integration.yml) | Seed TTS PCM/MP3/Ogg Opus streaming and batch; separate Tencent smoke test | Relevant push paths and manual dispatch |

The Alibaba ASR matrix consists of
`fun-asr-realtime`, `qwen-audio-3.1-asr-flash-streaming`,
`qwen-audio-3.1-asr-flash-message`,
`paraformer-realtime-v2`, and `paraformer-realtime-8k-v2`.
The workflow limits live requests to two model jobs at once.

Aliyun's **paid** Fun-ASR Opus step checks Ogg Opus passthrough and
raw Opus packet to Ogg remux without transcoding. The FFmpeg decoding
and native libopus decoder alternatives are exercised by separate offline
tests; do not count them as additional real-cloud Opus checks.

The TTS workflow includes a Tencent job whose **successful job conclusion
does not necessarily mean the cloud synthesis test passed**. When the
remote account returns `3022` (resource pack exhausted), the workflow
emits a warning and marks Tencent TTS audio **not validated**, while
other errors still fail the job. Read the job log or step summary.

## Credentials and cost

Each workflow gets its own secrets and variables via GitHub Actions.
For example, Aliyun ASR requires:

- Secret: `DASHSCOPE_API_KEY`.
- Variable: `DASHSCOPE_WS_ENDPOINT`, a regional
  `wss://.../api-ws/v1/inference` endpoint.

Other workflows document their environment variables directly. Do not
commit keys, signed WebSocket URLs, or user voice recordings to fixtures.
Real-cloud test executions can incur provider charges, and changing an
unrelated feature branch may still trigger workflows whose `push` filters
cover all branches.

## Local Aliyun ASR smoke test

Provide a real 16 kHz mono PCM16LE file, or the sample rate required by
the selected model:

```sh
export DASHSCOPE_API_KEY="..."
export DASHSCOPE_WS_ENDPOINT="wss://dashscope.aliyuncs.com/api-ws/v1/inference"
export DASHSCOPE_ASR_MODEL="fun-asr-realtime"
export ASR_TEST_PCM_FILE="/path/to/speech-16k-mono.pcm"
go test -tags=integration -run '^TestAliyunLiveASR$' -count=1 -v ./asr/providers/aliyun
```

For the paid Ogg Opus passthrough/remux test, see
`TestAliyunLiveOggPassthroughAndRemux` and the corresponding workflow.
For other providers, use their individual integration tests and
environment settings. Use synthetic audio for smoke tests and evaluate
recognition accuracy on representative application recordings separately.

The SDK does not impose a universal transcript accuracy threshold,
network throughput target, cloud concurrency limit, or application-level
telemetry.
