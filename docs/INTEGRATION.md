# Aliyun ASR online integration test

The standard `Go CI` workflow is **offline**: it uses mock WebSocket servers.
It does not read or spend an Alibaba Cloud API key.

A separate **manual** GitHub Actions workflow is available:
[Aliyun ASR Integration](../.github/workflows/aliyun-asr-integration.yml).

## Required repository configuration

GitHub → Settings → Secrets and variables → Actions

- **Secret** `DASHSCOPE_API_KEY`: a real Alibaba Cloud Model Studio API key.
- **Variable** `DASHSCOPE_WS_ENDPOINT`: the full matching regional WSS endpoint,
  e.g. `wss://dashscope.aliyuncs.com/api-ws/v1/inference`.

Keys and endpoints must belong to the same region. Do not put credentials in
repository variables, logs, issues, or source code.

## Manual execution

Actions → **Aliyun ASR Integration (paid, manual)** → **Run workflow** → select
one model. Each run makes a real billable ASR request. Nothing runs automatically
on push or PR. The workflow generates English speech using `espeak-ng`,
converts it to 16kHz or 8kHz mono PCM16LE and streams 100ms chunks to the
configured endpoint. It requires a nonempty final transcript and waits for
`task-finished` before finishing.

This verifies the authentication/transport/task lifecycle and real
speech-to-text response; it is **not** a measure of accuracy on children's
speech, Chinese accents, device microphones, noise, or latency under load.

## Local execution

With an audio fixture already encoded as signed 16-bit mono PCM (16kHz by
default; 8kHz for Paraformer 8k), set:

```sh
export DASHSCOPE_API_KEY="..."
export DASHSCOPE_WS_ENDPOINT="wss://dashscope.aliyuncs.com/api-ws/v1/inference"
export DASHSCOPE_ASR_MODEL="fun-asr-realtime"
export ASR_TEST_PCM_FILE="/path/to/speech-16k-mono.pcm"
go test -tags=integration -run '^TestAliyunLiveASR$' -v ./asr/providers/aliyun
```

**Do not** add this test to normal CI without consent and a spending cap.
