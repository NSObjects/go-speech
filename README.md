# go-speech

[![Go CI](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml/badge.svg)](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/NSObjects/go-speech.svg)](https://pkg.go.dev/github.com/NSObjects/go-speech)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

**面向 Go 应用的多厂商 ASR（语音识别）和 TTS（语音合成）SDK。**

通过统一接口接入阿里云、火山引擎和腾讯云。业务方使用强类型配置选择厂商，SDK 负责网络协议、流式会话、音频数据传输和错误归一化。

- **统一入口**：`speech.NewASR`、`speech.NewTTS`
- **流式优先**：ASR 支持持续上传音频和接收识别事件；TTS 支持逐块接收合成音频
- **明确生命周期**：`context.Context` 控制取消，`Close` 释放资源
- **不绑定业务**：不包含设备通信、VAD 决策、Agent/LLM、录音或播放器
- **无隐式转码与重试**：音频格式选择、跨厂商切换和失败重试由调用方控制

## 环境与安装

要求 **Go 1.27.4 或更高版本**。

```sh
go get github.com/NSObjects/go-speech@latest
```

## 厂商支持

| 厂商 | ASR | TTS | 说明 |
| --- | --- | --- | --- |
| 阿里云 | 流式识别 | Batch / Stream | DashScope ASR、CosyVoice |
| 火山引擎 | 流式识别 | Batch / Stream | 豆包 ASR、Seed TTS |
| 腾讯云 | 流式识别 | Batch / Stream | 实时 ASR、流式 TTS v2 |

以上表示**代码中已实现的接口**，不等于每个模型、音色和格式组合都通过真实云端验证。阿里云 CosyVoice 与火山引擎 Seed TTS 已有真实云端音频验证；腾讯云 TTS 的在线合成尚未完成验证。具体支持的音频格式与可用模型以各厂商文档及账号权限为准。

## TTS：语音合成

`speech.NewTTS` 返回 `tts.Client`，同时支持：

- `Synthesize(ctx, req)`：获得完整 `audio.Buffer`，适合短文本
- `SynthesizeStream(ctx, req)`：连续读取 `audio.Chunk`，适合低延迟处理或转发

以下示例使用阿里云 CosyVoice，完整读取流式音频并写入 MP3 文件：

```go
package main

import (
    "context"
    "errors"
    "io"
    "log"
    "os"
    "time"

    speech "github.com/NSObjects/go-speech"
    "github.com/NSObjects/go-speech/audio"
    "github.com/NSObjects/go-speech/tts"
)

func main() {
    client, err := speech.NewTTS(speech.AliyunTTS{
        APIKey:   os.Getenv("DASHSCOPE_API_KEY"),
        Endpoint: os.Getenv("DASHSCOPE_WS_ENDPOINT"),
    })
    if err != nil {
        log.Fatal(err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()

    stream, err := client.SynthesizeStream(ctx, tts.Request{
        Text:  "你好，欢迎使用语音合成。",
        Voice: "longanyang",
        Format: audio.Format{
            Encoding: audio.EncodingMP3,
            SampleRate: 22050,
            Channels: 1,
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    defer stream.Close()

    file, err := os.Create("speech.mp3")
    if err != nil {
        log.Fatal(err)
    }
    defer file.Close()

    for {
        chunk, err := stream.Recv(ctx)
        if errors.Is(err, io.EOF) {
            break
        }
        if err != nil {
            log.Fatal(err)
        }
        if _, err := file.Write(chunk.Data); err != nil {
            log.Fatal(err)
        }
    }
}
```

设置 `DASHSCOPE_API_KEY` 与对应地域的 `DASHSCOPE_WS_ENDPOINT` 后运行。文件仅在成功收到合成完成事件后才可视为完整；生产代码应使用临时文件，并在失败时删除不完整输出。

**切换厂商只需更换配置类型**，无需在业务中导入 `tts/providers/*`：

```go
// 阿里云
speech.NewTTS(speech.AliyunTTS{
    APIKey: key,
    Endpoint: endpoint,
})

// 火山引擎（使用新控制台 API Key）
speech.NewTTS(speech.VolcengineTTS{
    APIKey: key,
    ResourceID: "seed-tts-2.0",
})

// 腾讯云
speech.NewTTS(speech.TencentTTS{
    AppID: appID,
    SecretID: secretID,
    SecretKey: secretKey,
})
```

这些配置是独立的示例表达式；实际使用时应检查返回的 `client, err`。音色 ID **不跨厂商通用**，应根据所选 Provider 设置 `tts.Request.Voice`。

也可以直接运行 [TTS 厂商切换示例](examples/tts-switch)，默认生成 24 kHz mono MP3：

```sh
export TTS_PROVIDER=volcengine
export VOLCENGINE_API_KEY="..."
export TTS_VOICE=zh_female_vv_uranus_bigtts
go run ./examples/tts-switch "你好，欢迎使用语音合成。" speech.mp3

# 一次性合成
TTS_MODE=batch go run ./examples/tts-switch "你好。" speech.mp3
```

`TTS_PROVIDER` 也支持 `aliyun`、`tencent`，需要设置相应的凭据和厂商音色 ID。

## ASR：流式语音识别

`speech.NewASR` 返回 `asr.StreamRecognizer`。打开会话后：

1. `Send(ctx, bytes)` 上传音频；一个发送者和一个接收者可以并行运行。
2. `Recv(ctx)` 接收 `asr.EventPartial`（可能变化）与 `asr.EventFinal`（已确认文本片段）。
3. 上传结束后调用 `CloseSend(ctx)`，继续读取直到 `io.EOF`。
4. 始终调用 `Close()` 释放会话。

```go
client, err := speech.NewASR(speech.VolcengineASR{
    APIKey: apiKey,
})
if err != nil {
    return err
}

stream, err := client.OpenStream(ctx, asr.Config{
    Format: audio.Format{
        Encoding: audio.EncodingPCM16LE,
        SampleRate: 16000,
        Channels: 1,
    },
})
if err != nil {
    return err
}
defer stream.Close()

// 在发送协程中按实际采样率上传 PCM16LE 数据并调用 CloseSend(ctx)；
// 接收协程持续调用 Recv(ctx)，处理 EventPartial / EventFinal。
```

以上是 API 片段；完整可运行、包含音频发送速率控制和并行接收的程序见 [ASR 厂商切换示例](examples/asr-switch)。

```sh
export ASR_PROVIDER=volcengine
export VOLCENGINE_API_KEY="..."
go run ./examples/asr-switch speech.pcm
```

输入文件需要是**原始 16 kHz、单声道 PCM16LE**，不是 WAV 文件。`ASR_PROVIDER` 同样支持 `aliyun`、`tencent`；输入格式必须符合选用模型的要求。

## 音频格式与生命周期

`audio.Format` 明确描述编码、采样率与声道数。SDK 不会根据文件扩展名偷偷转换音频，也不会为不同厂商自动重采样。

- TTS 音频块是同一音频流的连续字节。**单个 MP3 或 Ogg Opus Chunk 不一定能独立解码**；保存文件时按接收顺序拼接。
- 阿里云 CosyVoice 可请求 PCM16LE、MP3、WAV、Ogg Opus；火山引擎 Seed TTS 可请求 PCM16LE、MP3、Ogg Opus，**Ogg Opus 仅支持 48 kHz mono**。不同模型和音色仍有额外限制。
- `audio.EncodingOpus` 表示 **Ogg Opus** 容器，不是裸 Opus Packet。
- TTS 请求一次提供完整文本；当前 `SynthesizeStream` 指的是**音频流式输出**，不是增量文本输入。
- 成功完成后，`Recv` 持续返回 `io.EOF`；主动提前关闭可通过 `errors.Is(err, tts.ErrStreamClosed)` 判断，取消与超时保留 `context` 错误。
- 失败后的 `Recv` 保留终止错误。不要在同一失败会话内盲目重放音频或合成请求。

可选音频工具包括 [FFmpeg 流式解码](audio/ffmpeg/)、[Opus](audio/codec/opus) 与 [AMR-NB](audio/codec/amrnb) 处理；原生编解码功能按需启用 CGo/build tags，核心 SDK 不强制依赖 FFmpeg。

详细约束见 [音频设计](docs/AUDIO.md)、[ASR 接口](asr/) 和 [TTS 接口](tts/)。

## 错误处理

厂商错误统一包装为 `speecherr.Error`，可通过 `speecherr.IsCode` 检查错误类别，并从 `RemoteCode` 查看厂商原始错误码。常见类别包括 `invalid_argument`、`unsupported`、`unauthorized`、`rate_limited`、`timeout`、`unavailable`、`protocol`。

`Retryable` 只提供建议；是否重试、退避及幂等保护由调用方决定。切换厂商、读取环境变量和加载配置同样由业务层负责。

## 测试与开发

```sh
go test ./...
go test -race ./...
go vet ./...
```

真实云端集成测试使用 `integration` build tag，需要相应的密钥、账号权限，且**可能产生费用**。项目的 GitHub Actions 包含独立的 [Go CI](.github/workflows/ci.yml)、[阿里云 TTS 集成测试](.github/workflows/aliyun-tts-integration.yml) 与 [火山引擎 TTS 集成测试](.github/workflows/tts-live-integration.yml)。查看当前执行结果请以 Actions 为准。

## 进一步阅读

- ASR Provider：[阿里云](asr/providers/aliyun/README.md) · [火山引擎](asr/providers/volcengine/README.md) · [腾讯云](asr/providers/tencent/README.md)
- TTS Provider：[阿里云 CosyVoice](tts/providers/aliyun/README.md) · [火山引擎 Seed TTS](tts/providers/volcengine/README.md) · [腾讯云](tts/providers/tencent/README.md)
- [SDK 设计](docs/DESIGN.md) · [音频处理](docs/AUDIO.md) · [集成测试](docs/INTEGRATION.md)

## License

[Apache License 2.0](LICENSE)
