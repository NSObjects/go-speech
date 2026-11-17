# Automated Aliyun ASR online integration

The ordinary `Go CI` workflow is offline (mock WebSocket and local codec tests).
The separate **Aliyun ASR Integration (automatic, paid)** workflow runs
**automatically for every push** (all branches), and optionally on manual
dispatch. The workflow deliberately has **no pull_request trigger**: the
separate Go CI checks PRs without secrets, and a branch push already runs
real ASR once without duplicate PR cloud charges.

## Repository configuration

Settings → Secrets and variables → Actions:

- Secret `DASHSCOPE_API_KEY`: an Alibaba Cloud Model Studio API key.
- Variable `DASHSCOPE_WS_ENDPOINT`: matching regional WSS endpoint, e.g.
  `wss://dashscope.aliyuncs.com/api-ws/v1/inference`.

The API key must be authorized to invoke the target model in that region.
Keys are never copied into test fixtures or committed to source.

## What runs

The workflow exercises **five models**, as separate jobs with at most two
concurrent live ASR requests:

- Fun-ASR-Realtime
- Qwen-Audio 3.1 ASR Streaming
- Qwen-Audio 3.1 ASR Message
- Paraformer-Realtime-v2
- Paraformer-Realtime-8k-v2

`espeak-ng` generates a short English utterance, `ffmpeg` generates 16kHz
mono PCM16LE (or 8kHz for Paraformer 8k), and the SDK sends real-time audio
chunks over authenticated WebSocket. The test waits for task completion,
requires a nonempty final transcript, and validates session lifecycle.

Fun-ASR additionally tests **four** Opus paths:

- **Ogg Opus → ASR** (unchanged, zero-transcode passthrough)
- **Raw Opus packets → pure-Go OggMuxer → ASR** (zero-transcode repackaging)
- **Ogg Opus → persistent FFmpeg decoder → PCM16LE → ASR**
- **Raw Opus packets → native libopus PacketDecoder → PCM16LE → ASR**

The direct paths are the recommended routes if the model accepts Ogg Opus.
The PCM paths remain optional compatibility fallbacks. All tests use the
same synthetic spoken-English fixture.

The fixtures contain synthetic speech, *not* real children's voices. They
verify connectivity and correctness, not accuracy, voice-device acoustic
quality, or production tail latency. Every invocation may incur provider
charges; the workflow intentionally does not skip paid requests.

## Local execution

Provide mono PCM16LE test audio at the proper sample rate:

```sh
export DASHSCOPE_API_KEY="..."
export DASHSCOPE_WS_ENDPOINT="wss://dashscope.aliyuncs.com/api-ws/v1/inference"
export DASHSCOPE_ASR_MODEL="fun-asr-realtime"
export ASR_TEST_PCM_FILE="/path/to/speech-16k-mono.pcm"
go test -tags=integration -run '^TestAliyunLiveASR$' -v ./asr/providers/aliyun
```

To run the decoded Opus pipeline locally, also set
`ASR_TEST_OPUS_FILE` to an Ogg Opus file and install FFmpeg.

CI automatically runs the online model matrix on every push.
Runs are not canceled or replaced by a shared concurrency group. You can
rerun individual failing jobs in GitHub Actions; no manual first-time trigger
is necessary.
