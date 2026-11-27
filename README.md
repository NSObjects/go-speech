# go-speech

[![Go CI](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml/badge.svg)](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/NSObjects/go-speech.svg)](https://pkg.go.dev/github.com/NSObjects/go-speech)
[![License](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

**go-speech** 是支持阿里云、火山引擎和腾讯云的 Go 语音识别（ASR）、语音合成（TTS）与音色管理 SDK。

## 功能

| 厂商 | ASR | TTS | 音色管理 |
| --- | --- | --- | --- |
| 阿里云 | 短音频、实时流式 | CosyVoice：一次性、流式输出、双向流式 | 复刻、设计、查询、更新、删除 |
| 火山引擎 | 短音频、实时流式 | Seed-TTS 3.0：一次性、流式输出、双向流式 | 复刻、设计、查询 |
| 腾讯云 | 短音频、实时流式 | WebSocket v2：一次性、流式输出、双向流式 | 训练文本、录音检测、复刻任务、查询 |

具体音频格式、模型、音色及高级参数支持情况见各厂商文档。
## 安装

需要 Go 1.27.4 或更新版本：

```bash
go get github.com/NSObjects/go-speech@latest
```

## 快速开始

以下命令在克隆仓库的根目录中执行：

```bash
git clone https://github.com/NSObjects/go-speech.git
cd go-speech
```

### TTS：文本转语音

使用火山引擎生成 MP3：

```bash
export TTS_PROVIDER=volcengine
export VOLCENGINE_API_KEY="your-api-key"
export VOLCENGINE_TTS_MODEL_ID="doubao-seed-tts-3.0"
export TTS_VOICE="your-seed3-voice-id"

go run ./examples/tts-switch "你好，欢迎使用 go-speech。" output.mp3
```

示例默认一次提交完整文本、流式读取音频；也可一次性收集完整音频：

```bash
TTS_MODE=batch go run ./examples/tts-switch "你好。" output.mp3
```

在 Go 代码中使用统一接口：

```go
client, err := speech.NewTTS(speech.VolcengineTTS{
    APIKey:  apiKey,
    ModelID: "doubao-seed-tts-3.0",
})
if err != nil {
    return err
}

result, err := client.Synthesize(ctx, tts.Request{
    Text:  "你好，欢迎使用 go-speech。",
    Voice: "your-seed3-voice-id",
    Format: audio.Format{
        Encoding:   audio.EncodingMP3,
        SampleRate: 24000,
        Channels:   1,
    },
})
if err != nil {
    return err
}
_ = result.Data // 完整音频字节
```

三种合成方式：

| 方法 | 文本输入 | 音频输出 |
| --- | --- | --- |
| `Synthesize` | 完整文本 | 完整 `audio.Buffer`（最多 64 MiB） |
| `SynthesizeStream` | 完整文本 | `tts.Stream` |
| `OpenDuplex` | 逐段文本 | `tts.DuplexStream` |

双向流式可直接通过 `speech.NewDuplexTTS` 创建客户端：

```go
client, err := speech.NewDuplexTTS(speech.VolcengineTTS{
    APIKey: apiKey,
})
if err != nil {
    return err
}

stream, err := client.OpenDuplex(ctx, tts.DuplexRequest{
    Voice: voiceID,
    Format: audio.Format{
        Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1,
    },
})
if err != nil {
    return err
}
defer stream.Close()
```

一个 goroutine 依次调用 `SendText(ctx, text)`、`CloseSend(ctx)`，另一个 goroutine 同时调用 `Recv(ctx)` 读取音频，直至 `io.EOF`。发送或接收失败时结束本次会话。所有流都需要调用 `Close()`。

需要完整音频及字词时间戳时，使用 `tts.SynthesizeDetailed(ctx, client, req)`。例如，火山引擎（`volcengine` 为 `tts/providers/volcengine` 包）：

```go
enabled := true
result, err := tts.SynthesizeDetailed(ctx, client, tts.Request{
    Text:  "你好。",
    Voice: voiceID,
    Options: tts.Options{
        Provider: volcengine.Options{EnableSubtitle: &enabled},
    },
})
if err != nil {
    return err
}
_ = result.Audio
_ = result.Alignments
```

阿里云通过 `aliyun.Options.WordTimestampEnabled` 开启时间戳，腾讯云通过 `tencent.Options.EnableSubtitle` 开启。实际返回内容取决于模型和音色；SDK 不会自动生成缺失的时间戳。需要边接收音频边处理对齐数据时，使用 `tts.EventReceiver`。

### ASR：音频转文字

准备 16 kHz、单声道 PCM16LE 原始音频文件：

```bash
ffmpeg -i input.wav -f s16le -acodec pcm_s16le -ac 1 -ar 16000 speech.pcm
```

直接识别完整短音频：

```go
client, err := speech.NewASR(speech.VolcengineASR{APIKey: apiKey})
if err != nil {
    return err
}

pcm, err := os.ReadFile("speech.pcm")
if err != nil {
    return err
}

result, err := client.Recognize(ctx, audio.Buffer{
    Data: pcm,
    Format: audio.Format{
        Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1,
    },
}, asr.Config{})
if err != nil {
    return err
}
fmt.Println(result.Text)
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

一个 goroutine 使用 `Send(ctx, data)` 发送音频，另一个 goroutine 同时使用 `Recv(ctx)` 接收识别事件。发送完毕后调用 `CloseSend(ctx)`，继续接收直到 `io.EOF`。`EventFinal` 是已确认的文本片段，不一定表示整段对话结束。启用 `asr.Options.UtteranceDetails` 后可读取厂商提供的分段、字词时间戳等元数据；`EventUtteranceUpdate` 用于更新已确认片段的元数据，不应重复追加文本。

## 选择厂商

通过不同的配置类型初始化相同接口：

| 厂商 | ASR 配置 | TTS 配置 |
| --- | --- | --- |
| 阿里云 | `speech.AliyunASR` | `speech.AliyunTTS` |
| 火山引擎 | `speech.VolcengineASR` | `speech.VolcengineTTS` |
| 腾讯云 | `speech.TencentASR` | `speech.TencentTTS` |

不同厂商使用各自的认证信息、模型和音色 ID。详见 [阿里云](asr/providers/aliyun/README.md)、[火山引擎](asr/providers/volcengine/README.md) 和 [腾讯云](asr/providers/tencent/README.md) ASR 文档，以及各自的 [阿里云 TTS](tts/providers/aliyun/README.md)、[火山引擎 TTS](tts/providers/volcengine/README.md)、[腾讯云 TTS](tts/providers/tencent/README.md) 文档。

## 音频格式

使用 `audio.Format` 指定编码格式、采样率和声道数。支持的组合取决于厂商及模型。

TTS 未显式指定格式时使用厂商默认值；不同厂商的默认值可能不同。SDK 不会自动转码或重采样。音频块应按接收顺序处理。

## 测试

```bash
go test ./...
go test -race ./...
go vet ./...
```

## 文档

- [API 设计与接口约定](docs/DESIGN.md)
- [音频处理](docs/AUDIO.md)
- [集成测试](docs/INTEGRATION.md)
- [Go API Reference](https://pkg.go.dev/github.com/NSObjects/go-speech)

## 高级选项与能力检查

通过 `asr.Config.Options`、`tts.Request.Options` 或 `tts.DuplexRequest.Options` 设置请求级参数。通用选项与厂商原生 `Options` 可组合使用；原生参数详见对应厂商文档。

内置客户端实现可选接口 `asr.CapabilityProvider`、`tts.CapabilityProvider`、`asr.ConfigValidator` 和 `tts.RequestValidator`。能力查询与参数校验均在本地执行，不发送网络请求。TTS 分别通过 `ValidateRequest`、`ValidateSynthesize`、`ValidateDuplex` 检查不同合成方式的参数组合。

`Capabilities().Features.Get(...)` 返回具体能力及参数描述。unknown 表示尚未确认或依赖具体模型、音色或服务权限。通过本地校验不代表云端已授权。

## 音色管理

- [阿里云](voice/providers/aliyun)：CosyVoice / Qwen Audio 音色复刻、设计及管理。
- [火山引擎](voice/providers/volcengine)：Seed-TTS 3.0 音色复刻、设计与查询。
- [腾讯云](voice/providers/tencent)：VRS 训练文本、录音检测、复刻任务与音色查询。

音色管理使用独立客户端。合成时传入厂商返回的音色 ID，并选择对应模型。

## 连接池

连接池按 Client 隔离，默认关闭。支持范围：

| 厂商 | ASR 连接复用 | TTS 连接复用 | TTS 显式预热 |
| --- | --- | --- | --- |
| 阿里云 | 支持 | 支持 | `Warmup(ctx, "", count)` |
| 火山引擎 | 不支持 | 仅 Seed-TTS 3.0 WebSocket 双向流式 | `Warmup(ctx, voiceID, count)` |
| 腾讯云 | 不支持 | 不支持 | 提前调用 `OpenDuplex` |

按需设置 `Pool`：

```go
client, err := speech.NewTTS(speech.AliyunTTS{
    APIKey: apiKey,
    Pool: &speech.PoolConfig{
        MaxOpen: 16,
        MaxIdle: 4,
    },
})
if err != nil {
    return err
}
defer client.(io.Closer).Close()
```

在多次调用间复用客户端。正常合成无需查询能力或手动获取连接。需要主动预热时，直接调用：

```go
if err := tts.Warmup(ctx, client, voiceID, 1); err != nil {
    return err
}
```

阿里云忽略音色 ID，火山需要实际音色 ID；不支持预热的客户端返回 `CodeUnsupported`。需要动态展示配置时，才查询 `client.(tts.CapabilityProvider).Capabilities().Pool`。ASR 同样可通过 `asr.CapabilityProvider` 查询 `Capabilities().Pool`。`Supported` 表示支持，`Enabled` 表示已配置且未关闭；`Batch`、`Streaming`、`Duplex` 表示复用适用的调用方式，而非当前网络状态。能力查询不发起网络请求。

火山需传实际音色 ID。预热数量是目标空闲连接数，而非每次新增数量；预热仅建立 WebSocket，不启动合成任务，不保证后续一定有空闲连接。腾讯可提前打开实际请求的双向流式会话，使用同一会话发送文本并接收音频。会话绑定传入的 Context，未使用也须关闭。

连接池不自动预热、补充空闲连接、保活或重试任务。详见 [连接管理](docs/connection-management.md)、[阿里连接池](docs/aliyun-connection-pool.md) 和 [火山双向流式连接池](tts/providers/volcengine/README.md#optional-duplex-connection-reuse)。

## 错误处理

`speecherr.Error` 提供统一的错误分类、厂商原始错误码和重试提示；`speecherr.IsCode` 可判断错误类别，`errors.Is` 和 `errors.As` 可检查原始错误。SDK 不自动重试或切换厂商。

## License

[Apache-2.0](LICENSE)
