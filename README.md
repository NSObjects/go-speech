# go-speech

[![Go CI](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml/badge.svg)](https://github.com/NSObjects/go-speech/actions/workflows/ci.yml)

A vendor-independent ASR and TTS SDK for Go. **ASR adapters: Alibaba Cloud and Volcengine Doubao streaming WebSocket; TTS adapter: Alibaba Cloud CosyVoice WebSocket.**

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

> ASR 已包含阿里云五模型及火山引擎豆包流式 ASR Provider。TTS 已新增阿里云 CosyVoice WebSocket Provider（真实云端验证情况以 GitHub Actions 为准）。

## 快速开始：使用统一的 ASR 入口

`speech.NewASR` 使用 **配置类型** 选择厂商，并返回
`asr.StreamRecognizer`。业务代码无需导入 `asr/providers/*`，
也不需要 SDK 读取 YAML、环境变量或设置全局 Provider。

```go
import (
    speech "github.com/NSObjects/go-speech"
    "github.com/NSObjects/go-speech/asr"
    "github.com/NSObjects/go-speech/audio"
)

// 只需提供当前使用的厂商配置：
recognizer, err := speech.NewASR(speech.TencentASR{
    AppID: appID, SecretID: secretID, SecretKey: secretKey,
    EngineModel: "16k_zh_en_2.0", NeedVAD: true,
})
if err != nil { return err }

stream, err := recognizer.OpenStream(ctx, asr.Config{
    Format: audio.Format{
        Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1,
    },
})
if err != nil { return err }
defer stream.Close()
// One sender calls Send then CloseSend; a concurrent receiver calls Recv
// until io.EOF. EventPartial is tentative; EventFinal is confirmed text.
```

**按变量切换**：调用方决定变量的来源，并选择相应的强类型配置。
`NewASR` 本身只创建 Client，不打开远程连接。

```go
configs := map[string]speech.ASRConfig{
    "tencent": speech.TencentASR{
        AppID: appID, SecretID: secretID, SecretKey: secretKey,
        EngineModel: "16k_zh_en_2.0",
    },
    "aliyun": speech.AliyunASR{
        APIKey: dashscopeKey,
        Endpoint: "wss://dashscope.aliyuncs.com/api-ws/v1/inference",
    },
    "volcengine": speech.VolcengineASR{
        APIKey: volcengineKey,
    },
}
cfg, ok := configs[providerName]
if !ok { return fmt.Errorf("unknown ASR provider %q", providerName) }
recognizer, err := speech.NewASR(cfg)
```

也支持传入 `*speech.TencentASR` 等指针配置。配置类型直接复用
现有 Provider 的字段定义，不维护另一套配置参数。三个 Provider 的
音频格式、模型能力和 VAD 设置仍各自生效；Factory 不进行隐式转码、
自动重试或失败自动切换。需要自行扩展其他 Provider 时，仍可使用
`asr.Registry` 或实现 `asr.StreamRecognizer`。

**完整可运行的 PCM 流式例子：** [examples/asr-switch](examples/asr-switch)。
设置 `ASR_PROVIDER=tencent` / `aliyun` / `volcengine` 和对应鉴权
环境变量，准备原始 16 kHz mono PCM16LE 文件，然后运行：

```sh
go run ./examples/asr-switch speech.pcm
```

## 快速开始：三家 TTS 厂商切换

`speech.NewTTS` 和 `speech.NewASR` 使用相同的强类型配置思路。
返回值同时实现 `tts.Synthesizer`（完整音频）和
`tts.StreamSynthesizer`（边合成边接收音频）。

```go
configs := map[string]speech.TTSConfig{
    "aliyun": speech.AliyunTTS{
        APIKey: dashscopeKey,
        Endpoint: "wss://dashscope.aliyuncs.com/api-ws/v1/inference",
        Model: "cosyvoice-v3-flash",
    },
    "tencent": speech.TencentTTS{
        AppID: appID, SecretID: secretID, SecretKey: secretKey,
    },
    "volcengine": speech.VolcengineTTS{
        APIKey: volcengineKey,
        ResourceID: "seed-tts-2.0",
    },
}
config, ok := configs[providerName]
if !ok { return fmt.Errorf("unknown TTS provider %q", providerName) }

synth, err := speech.NewTTS(config)
if err != nil { return err }
stream, err := synth.SynthesizeStream(ctx, tts.Request{
    Text: "你好，欢迎使用语音合成。",
    Voice: providerVoiceID, // 厂商自己的音色 ID
    Format: audio.Format{
        Encoding: audio.EncodingMP3, SampleRate: 24000, Channels: 1,
    },
})
if err != nil { return err }
defer stream.Close()

for {
    chunk, err := stream.Recv(ctx)
    if errors.Is(err, io.EOF) { break }
    if err != nil { return err }
    consume(chunk.Data) // 业务方负责播放/转发，SDK 不播放音频
}
```

上例展示接口，不表示三家的每个音色都支持相同采样率。
通过各 Provider 的 `SupportedAudio` 能力与音色实际规格选择格式。
`NewTTS` 不读取 YAML、环境变量，不暗中转换音频，也不会自动重试。
腾讯云、火山引擎 TTS 的**本地协议测试与真实付费 API 验证是两回事**；
使用前需要激活对应 TTS 资源并完成真实云端测试。
细节参见 [腾讯云 TTS](tts/providers/tencent/README.md)、
[火山引擎 TTS](tts/providers/volcengine/README.md)、
[阿里云 TTS](tts/providers/aliyun/README.md)。

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

## Recommended Opus path: no decode/re-encode

When the selected ASR model accepts Ogg Opus, forward existing Ogg Opus
bytes directly. For device transport that sends **complete raw Opus packets**,
the new pure-Go `opus.OggMuxer` produces a valid streaming Ogg Opus container:
`Headers() → WritePacket(packet) → Close()`. It does not decode to PCM or use
FFmpeg/CGo, and the original compressed Opus payload is unchanged.
See [docs/AUDIO.md](docs/AUDIO.md) and the automatic paid integration tests
for both Ogg passthrough and raw-packet repackaging.

## ASR stream failure handling

Streaming adapters now expose shared `asr.ErrStreamClosed` and
`asr.ErrInputClosed` errors, preserve cancellation, and keep the original
terminal `Recv` error visible on repeated calls. Malformed remote protocol
events use `speecherr.CodeProtocol`; the original remote service code is
available in `speecherr.Error.RemoteCode`. Do not retry audio on the same
failed session: open a new session and decide at the application layer if
replaying audio is safe. See [docs/DESIGN.md](docs/DESIGN.md).

## 火山引擎 Doubao ASR

新增 [`asr/providers/volcengine`](asr/providers/volcengine/)：支持官方推荐的
V3 `bigmodel_async` WebSocket 二进制协议、PCM16/WAV/MP3/Ogg Opus 原样传输、
流式部分/最终结果、Context 取消和统一错误。详细接入方式见
[火山引擎 Provider 文档](asr/providers/volcengine/README.md)。

## ASR format compatibility and errors

Validate the actual input format before opening a stream:
`aliyun.SupportedAudio(model)` for a specific Alibaba model, or
`volcengine.SupportedAudio()` for Seed-ASR 2.0's optimized streaming
endpoint. Both return `audio.Capability`, whose `Validate` method checks
supported encoding, sample rate, and channel count without transcoding.

Unsupported input formats return `speecherr.CodeUnsupported`, and
`errors.Is(err, audio.ErrUnsupportedFormat)` is true. Invalid format metadata
instead returns `speecherr.CodeInvalidArgument` and preserves
`audio.ErrInvalidFormat`. Feature incompatibility (for example, a model
parameter that the vendor does not implement) remains a separate
`CodeUnsupported` error and does not claim to be an audio-format error.

## Aliyun CosyVoice TTS

`tts/providers/aliyun` implements both batch and streaming TTS against
CosyVoice WebSocket. It sends complete text once, then emits binary audio
chunks without implicit conversion. See [TTS Provider README](tts/providers/aliyun/README.md).

## Tencent Cloud real-time ASR

`asr/providers/tencent` implements Tencent Cloud real-time ASR
WebSocket V2 with HMAC-SHA1 URL signing, PCM16LE mono streaming and
partial/final transcript events using the existing `asr.Stream` interface.
Tencent's proprietary Opus frame format is **not** compatible with
`audio.EncodingOpus` (Ogg Opus), so no implicit conversion is attempted.
See [Tencent ASR guide](asr/providers/tencent/README.md). A real cloud
integration test requires `TENCENT_APP_ID`, `TENCENT_SECRET_ID`, and
`TENCENT_SECRET_KEY` in GitHub Actions. Tencent TTS is a separate
future provider, not part of this change.
