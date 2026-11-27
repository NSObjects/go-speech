# go-speech

[![Go CI](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml/badge.svg)](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/NSObjects/go-speech.svg)](https://pkg.go.dev/github.com/NSObjects/go-speech)
[![License](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)

**go-speech** 是支持阿里云、火山引擎和腾讯云的 Go 语音识别（ASR）与语音合成（TTS）SDK，提供统一接口和流式处理。

## 功能

| 厂商 | ASR | TTS |
| --- | --- | --- |
| 阿里云 | 短音频识别、流式识别 | 一次性合成、流式合成（CosyVoice） |
| 火山引擎 | 短音频识别、流式识别 | 一次性合成、流式合成（Seed-TTS 3.0） |
| 腾讯云 | 短音频识别、流式识别 | 一次性合成、流式合成（WebSocket v2） |


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

默认使用流式合成。一次性合成：

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

`SynthesizeStream` 返回 `tts.Stream`，通过 `Recv(ctx)` 逐块读取音频，读取结束返回 `io.EOF`。使用后调用 `Close()`。

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

使用 `Send(ctx, data)` 发送音频，使用 `Recv(ctx)` 接收 `asr.EventPartial` 和 `asr.EventFinal`。发送完毕后调用 `CloseSend(ctx)`，继续接收直到 `io.EOF`。

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

## License

[Apache-2.0](LICENSE)

## Offline capability queries and request validation

Built-in clients implement optional `asr.CapabilityProvider` / `tts.CapabilityProvider` and `asr.ConfigValidator` / `tts.RequestValidator`. For example, `client.(asr.ConfigValidator).ValidateConfig(cfg)` validates a streaming configuration without a network request. For TTS, `ValidateRequest(req)` checks complete-text streaming/detailed requests, `ValidateSynthesize(req)` additionally checks audio-only batch restrictions, and `ValidateDuplex(req)` checks incremental-text configuration. The actual request paths reuse the same preparation logic. `Capabilities()` exposes local protocol paths, model/resource selection, coarse audio support, accepted text formats and native TTS controls. A successful local check is not a guarantee of account authorization, service availability or voice-specific support. No remote probing or automatic fallback occurs.

Advanced feature queries use `client.(asr.CapabilityProvider).Capabilities().Features.Get(asr.FeatureEmotion)` or `client.(tts.CapabilityProvider).Capabilities().Features.Get(tts.FeatureSpeed)`. Each feature has a local SDK/model status, optional parameter descriptors (Go field paths, native units, inclusive numeric ranges and enums), and notes. `unknown` means unconfirmed or dependent on voice/model/service behavior, not unsupported. An absent feature also returns unknown. The descriptor set covers common advanced controls, not every native subfield; use the typed options and offline validator for complete request combinations. No external requests are made. Returned snapshots are independent.

## 音色管理

火山 Seed-TTS 3.0 音色注册、查询与设计使用独立的 [`voice/providers/volcengine`](voice/providers/volcengine) Client；返回的 SpeakerID 可传入 TTS 的 Voice 字段。详见该包的使用说明。
