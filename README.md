# go-speech

[![Go CI](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml/badge.svg)](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml)

A vendor-independent ASR and TTS SDK for Go. **Early ASR adapter: Alibaba Cloud WebSocket models (see below); no TTS adapter yet.**

面向 Go 应用的多厂商语音识别（ASR）和语音合成（TTS）库。库本身与 Eino、LLM、智能体及具体语音设备无关。

## 设计目标

- **独立能力**：ASR、TTS 分别是独立 package，可按需使用。
- **多厂商**：通过 Provider 接口添加阿里云、火山引擎、腾讯云等实现。
- **批量与流式**：Batch 和 Streaming 是可选能力，厂商无须强制实现两者。
- **可取消**：网络操作使用 `context.Context`，流式会话显式关闭。
- **统一错误**：使用 `speecherr.Error` 分类错误并保留底层错误。
- **不预设业务**：不包含 VAD、Agent、MQTT、设备播放或对话 FSM。

## 结构

```text
go-speech/
├── asr/       # ASR contracts and provider registry
├── tts/       # TTS contracts and provider registry
├── audio/     # shared audio representation
├── speecherr/ # normalized provider errors
├── docs/      # design notes
└── .github/   # CI
```

## Go Module

```bash
go get github.com/NSObjects/go-speech@latest
```

> ASR 已包含阿里云 Provider：支持 Qwen-Audio 3.1 Streaming / Message、Fun-ASR-Realtime 和 Paraformer v2（含 8 kHz）。TTS 目前仍仅有公共接口；生产接入前需使用真实账号和录音进行端到端验证。

## 主要 API

```go
// ASR: implement asr.Recognizer for one-shot recognition,
// and/or asr.StreamRecognizer for streaming recognition.
// asr.Registry maps names to those optional capabilities.

// TTS: implement tts.Synthesizer and/or tts.StreamSynthesizer.
// tts.Registry maps names to those optional capabilities.

// audio.Format describes encoding, sample rate, and channel count.
```

流式 ASR 的 `EventFinal` 是确认的文本片段，不一定意味着整轮发言结束；VAD/turn endpointing 应由上层处理。流式 TTS 暂时接收完整文本，未来可额外扩展增量文本输入接口。

具体用法参见 [阿里云 ASR Adapter](asr/providers/aliyun/README.md)。

更多设计约束参见 [docs/DESIGN.md](docs/DESIGN.md)。

## 测试

```bash
go test -race ./...
go vet ./...
```

## License

Apache-2.0.

## Audio formats

The [`audio`](audio/) package now provides format detection, PCM16 WAV reading/writing,
mono/stereo conversion, a stateful PCM16 streaming resampler, and model capability
validation. Compressed audio is **passed through**, not decoded/transcoded.
See [docs/AUDIO.md](docs/AUDIO.md) for supported codecs, Ogg requirements,
chunk handling, and ASR integration examples.

## Optional compressed audio transcoding

Complete compressed audio buffers can be converted to PCM16LE or WAV by the
opt-in [`audio/ffmpeg`](audio/ffmpeg/) adapter, which requires an installed
FFmpeg executable. Core `audio` remains Go-only. The same optional package also provides a
**persistent** `ffmpeg.OpenStreamDecoder` that accepts successive Ogg Opus,
Ogg Speex, AMR-NB, MP3, AAC or PCM/WAV fragments and outputs streaming PCM16LE.
It is not a raw-Opus-packet decoder; see [audio design](docs/AUDIO.md).

## Real Aliyun ASR integration

The offline CI needs no cloud credentials. An [automatic, paid Aliyun ASR integration workflow](.github/workflows/aliyun-asr-integration.yml)
runs on every push, testing five ASR models with your `DASHSCOPE_API_KEY` secret and `DASHSCOPE_WS_ENDPOINT` variable to exercise real ASR with synthetic spoken audio. See [docs/INTEGRATION.md](docs/INTEGRATION.md).

## Raw device packet decoding (optional)

`audio/codec/opus` and `audio/codec/amrnb` now provide optional native
packet-level decoders. Raw Opus decoding is backed by libopus; AMR-NB frame
decoding uses OpenCORE-AMR-NB. Both are compiled in only under explicit
`libopus` / `opencoreamr` build tags with CGo. The default build remains
free of system codec dependencies.

The `amrnb.Framer` pure-Go utility also packages storage frames for the
existing persistent FFmpeg decoder. See [docs/AUDIO.md](docs/AUDIO.md)
for raw-packet vs Ogg vs RTP distinctions and setup.
