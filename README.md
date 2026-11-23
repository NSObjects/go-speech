# go-speech

[![Go CI](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml/badge.svg)](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/NSObjects/go-speech.svg)](https://pkg.go.dev/github.com/NSObjects/go-speech)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

**Go SDK for speech recognition (ASR) and text-to-speech (TTS) across multiple cloud providers.**

统一的 Go 接口接入阿里云、火山引擎和腾讯云。使用强类型配置选择厂商，无需在业务代码中直接处理各家的 WebSocket 协议。

- **ASR**：实时上传音频，接收增量与最终识别结果
- **TTS**：一次性合成或流式接收音频
- **统一调用方式**：`speech.NewASR` / `speech.NewTTS`
- **明确的行为**：支持 `context` 取消、流资源释放、统一错误分类；不隐式转码或自动重试

> 这是语音能力 SDK，不是语音对话框架；录音、VAD、设备通信、播放和 Agent 编排由应用自行负责。

## 支持情况

| Provider | ASR | TTS | 云端验证 |
| --- | --- | --- | --- |
| 阿里云 DashScope | Streaming | Batch / Streaming（CosyVoice） | ASR、TTS 已验证 |
| 火山引擎 | Streaming | Batch / Streaming（Seed TTS） | ASR、TTS 已验证 |
| 腾讯云 | Streaming | Batch / Streaming（WebSocket v2） | ASR 已验证；TTS 尚未完成真实合成验证 |

表中 Streaming TTS 表示**完整文本输入、音频流式输出**，不是增量文本输入。接口已实现不代表所有模型、音色、编码组合均已验证。腾讯云 TTS 目前有本地协议测试，但不能视为已完成真实云端合成验证。

## 安装

要求 **Go 1.27.4+**：

```bash
go get github.com/NSObjects/go-speech@latest
```

## Quick Start

仓库提供可以直接运行的 [ASR 示例](examples/asr-switch) 和 [TTS 示例](examples/tts-switch)。以下使用火山引擎展示调用过程；切换厂商只需更换配置和该厂商支持的模型、音色与音频参数。

### 1. TTS：文本转语音

设置火山引擎的 API Key、TTS 资源 ID 和音色 ID（须与账号开通的服务一致）：

```bash
export TTS_PROVIDER=volcengine
export VOLCENGINE_API_KEY="your-api-key"
export VOLCENGINE_TTS_RESOURCE_ID="seed-tts-2.0"
export TTS_VOICE="zh_female_vv_uranus_bigtts"

go run ./examples/tts-switch "你好，欢迎使用 go-speech。" output.mp3
```

生成的 `output.mp3` 为 24 kHz、单声道 MP3。示例默认流式接收音频；使用 `TTS_MODE=batch` 可以改成一次性合成：

```bash
TTS_MODE=batch go run ./examples/tts-switch "你好。" output.mp3
```

业务代码调用的核心 API：

```go
client, err := speech.NewTTS(speech.VolcengineTTS{
    APIKey:     apiKey,
    ResourceID: "seed-tts-2.0",
})
if err != nil {
    return err
}

buffer, err := client.Synthesize(ctx, tts.Request{
    Text:  "你好，欢迎使用 go-speech。",
    Voice: "zh_female_vv_uranus_bigtts",
    Format: audio.Format{
        Encoding:   audio.EncodingMP3,
        SampleRate: 24000,
        Channels:   1,
    },
})
if err != nil {
    return err
}
// buffer.Data 是完整音频字节；buffer.Format 描述实际音频格式。
```

需要边生成边消费音频时，使用 `client.SynthesizeStream(ctx, req)`，循环调用 `stream.Recv(ctx)`，直到 `io.EOF`。结束后始终调用 `stream.Close()`。

### 2. ASR：音频转文字

示例读取 **16 kHz / 单声道 / PCM16LE 原始音频文件**，按实时速率发送，并打印识别结果。输入是 `.pcm` 裸数据，不是带 WAV 文件头的 `.wav`：

```bash
export ASR_PROVIDER=volcengine
export VOLCENGINE_API_KEY="your-api-key"

go run ./examples/asr-switch speech.pcm
```

可以使用 FFmpeg 将现有音频转成示例需要的格式（这是应用预处理，不是 SDK 自动转码）：

```bash
ffmpeg -i input.wav -f s16le -acodec pcm_s16le -ac 1 -ar 16000 speech.pcm
```

核心 API：

```go
client, err := speech.NewASR(speech.VolcengineASR{APIKey: apiKey})
if err != nil {
    return err
}
stream, err := client.OpenStream(ctx, asr.Config{
    Format: audio.Format{
        Encoding:   audio.EncodingPCM16LE,
        SampleRate: 16000,
        Channels:   1,
    },
})
if err != nil {
    return err
}
defer stream.Close()

// 发送方：stream.Send(ctx, audioBytes)，最后 stream.CloseSend(ctx)
// 接收方：并行调用 stream.Recv(ctx)，处理 EventPartial / EventFinal
// 完整的并发发送、接收和发送速率控制见 examples/asr-switch。
```

`EventPartial` 是可能变化的识别结果，`EventFinal` 是已确认片段，**不一定代表整轮说话结束**。

## 切换厂商

无需修改 ASR/TTS 业务接口，只需传入不同配置类型：

| Provider | ASR 配置 | TTS 配置 |
| --- | --- | --- |
| 阿里云 | `speech.AliyunASR` | `speech.AliyunTTS` |
| 火山引擎 | `speech.VolcengineASR` | `speech.VolcengineTTS` |
| 腾讯云 | `speech.TencentASR` | `speech.TencentTTS` |

配置值由**调用方**传入。SDK 不自动读取 YAML、环境变量或创建全局 Provider 注册表。不同厂商的认证字段、模型、音色及音频能力不完全相同，请使用各自的配置和能力约束：

- [阿里云 ASR](asr/providers/aliyun/README.md) / [TTS](tts/providers/aliyun/README.md)
- [火山引擎 ASR](asr/providers/volcengine/README.md) / [TTS](tts/providers/volcengine/README.md)
- [腾讯云 ASR](asr/providers/tencent/README.md) / [TTS](tts/providers/tencent/README.md)

## 使用时需要知道的约定

| 主题 | 约定 |
| --- | --- |
| 音频格式 | 显式指定编码、采样率和声道数；不隐式转码、重采样 |
| TTS 默认格式 | `audio.Format{}` 选择所选厂商的默认格式，**各厂商不保证一致** |
| Opus | `audio.EncodingOpus` 表示 **Ogg Opus**，不是裸 Opus Packet |
| 流式音频 | `Chunk.Data` 是同一个音频流的连续字节；单块压缩数据不保证可独立解码 |
| 完成 | 成功结束的 `Recv` 返回 `io.EOF`；提前关闭、取消、失败有独立错误 |
| 生命周期 | 每个流结束时调用 `Close()`；ASR 用 `CloseSend()` 表示音频发送完毕 |
| 并发 | ASR 支持一个发送者和一个接收者并行；不要对同一流并发调用多个 `Recv` |
| 重试 | 不自动重放音频、不自动跨厂商切换，避免重复识别、合成及计费 |

厂商错误统一为 `speecherr.Error`；可以通过 `speecherr.IsCode` 检查错误类别，或通过 `errors.Is` / `errors.As` 处理具体原因。`Retryable` 仅供应用层参考。

详细设计见 [公开 API 与生命周期](docs/DESIGN.md)、[音频处理](docs/AUDIO.md)。

## 测试与贡献

```bash
go test ./...
go test -race ./...
go vet ./...
```

常规 CI 使用本地与模拟协议测试；真实云端集成测试需要密钥、服务权限，**可能产生费用**，且验证范围不等于所有模型与音色。参见 [集成测试说明](docs/INTEGRATION.md) 和 [GitHub Actions](https://github.com/NSObjects/go-speech/actions)。

## License

[Apache-2.0](LICENSE)
