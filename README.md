# go-speech

[![Go CI](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml/badge.svg)](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml)

A vendor-independent ASR and TTS SDK for Go. **Early foundation: provider adapters are not implemented yet.**

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

> 注意：当前只提供公共接口与 Registry，还没有可直接调用的厂商 Provider。接入厂商后再用于实际识别与合成。

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

更多设计约束参见 [docs/DESIGN.md](docs/DESIGN.md)。

## 测试

```bash
go test -race ./...
go vet ./...
```

## License

Apache-2.0.
