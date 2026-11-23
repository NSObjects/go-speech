# go-speech

[![Go CI](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml/badge.svg)](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/NSObjects/go-speech.svg)](https://pkg.go.dev/github.com/NSObjects/go-speech)
[![License](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

**go-speech** 是一个支持多云厂商的 Go 语音识别（ASR）与语音合成（TTS）SDK，提供统一的接口和流式处理能力。

支持阿里云、火山引擎和腾讯云，通过配置选择服务商。

## 功能

| 厂商 | ASR | TTS |
| --- | --- | --- |
| 阿里云 | 流式识别 | 一次性合成、流式合成（CosyVoice） |
| 火山引擎 | 流式识别 | 一次性合成、流式合成（Seed TTS） |
| 腾讯云 | 流式识别 | 一次性合成、流式合成（WebSocket v2） |

- **ASR**：发送音频数据，接收实时识别结果。
- **TTS**：将文本合成为完整音频，或逐块接收合成音频。
- **音频处理**：提供音频格式描述及可选的编解码工具。
- **错误处理**：统一错误分类，支持 `context.Context` 取消和超时。

各厂商的模型、音色与音频格式支持情况见下方厂商文档。

## 安装

需要 Go 1.27.4 或更新版本：

```bash
go get github.com/NSObjects/go-speech@latest
```

## 快速开始

### TTS：文本转语音

使用火山引擎生成 MP3：

```bash
export TTS_PROVIDER=volcengine
export VOLCENGINE_API_KEY="your-api-key"
export VOLCENGINE_TTS_RESOURCE_ID="seed-tts-2.0"
export TTS_VOICE="zh_female_vv_uranus_bigtts"

go run ./examples/tts-switch "你好，欢迎使用 go-speech。" output.mp3
```

默认使用流式合成。一次性合成：

```bash
TTS_MODE=batch go run ./examples/tts-switch "你好。" output.mp3
```

在 Go 代码中使用统一接口：

```go
client, err := speech.NewTTS(speech.VolcengineTTS{
    APIKey:     apiKey,
    ResourceID: "seed-tts-2.0",
})
if err != nil {
    return err
}

result, err := client.Synthesize(ctx, tts.Request{
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
// result.Data 包含完整音频数据
```

`SynthesizeStream` 返回 `tts.Stream`，通过 `Recv(ctx)` 逐块读取音频，读取结束返回 `io.EOF`。使用后调用 `Close()`。

### ASR：音频转文字

准备 16 kHz、单声道 PCM16LE 原始音频文件：

```bash
ffmpeg -i input.wav -f s16le -acodec pcm_s16le -ac 1 -ar 16000 speech.pcm
```

运行流式识别示例：

```bash
export ASR_PROVIDER=volcengine
export VOLCENGINE_API_KEY="your-api-key"

go run ./examples/asr-switch speech.pcm
```

在 Go 代码中创建识别流：

```go
client, err := speech.NewASR(speech.VolcengineASR{
    APIKey: apiKey,
})
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
```

使用 `Send(ctx, data)` 发送音频，使用 `Recv(ctx)` 接收 `asr.EventPartial` 和 `asr.EventFinal`。发送完毕后调用 `CloseSend(ctx)`，继续接收直到 `io.EOF`。

完整代码见 [ASR 示例](examples/asr-switch) 和 [TTS 示例](examples/tts-switch)。

## 选择厂商

通过不同的配置类型初始化相同接口：

| 厂商 | ASR 配置 | TTS 配置 |
| --- | --- | --- |
| 阿里云 | `speech.AliyunASR` | `speech.AliyunTTS` |
| 火山引擎 | `speech.VolcengineASR` | `speech.VolcengineTTS` |
| 腾讯云 | `speech.TencentASR` | `speech.TencentTTS` |

不同厂商使用各自的认证信息、模型及音色 ID。具体配置见：

| 厂商 | ASR 文档 | TTS 文档 |
| --- | --- | --- |
| 阿里云 | [ASR](asr/providers/aliyun/README.md) | [TTS](tts/providers/aliyun/README.md) |
| 火山引擎 | [ASR](asr/providers/volcengine/README.md) | [TTS](tts/providers/volcengine/README.md) |
| 腾讯云 | [ASR](asr/providers/tencent/README.md) | [TTS](tts/providers/tencent/README.md) |

## 音频格式

使用 `audio.Format` 指定编码格式、采样率和声道数。支持的组合取决于厂商及模型。

TTS 未显式指定格式时使用厂商默认值；不同厂商的默认值可能不同。SDK 不会自动转码或重采样。音频块应按接收顺序处理。

更多信息见 [音频处理文档](docs/AUDIO.md)。

## 测试

```bash
go test ./...
go test -race ./...
go vet ./...
```

云端集成测试说明见 [docs/INTEGRATION.md](docs/INTEGRATION.md)。

## 文档

- [API 设计与接口约定](docs/DESIGN.md)
- [音频处理](docs/AUDIO.md)
- [集成测试](docs/INTEGRATION.md)
- [Go API Reference](https://pkg.go.dev/github.com/NSObjects/go-speech)

## License

[Apache-2.0](LICENSE)
