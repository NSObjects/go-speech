# Alibaba Cloud ASR adapter

A single `aliyun.Client` supports these DashScope WebSocket task models:

| Model | Purpose | Audio sample rate |
|---|---|---|
| `qwen-audio-3.1-asr-flash-streaming` | Live recognition | Flexible |
| `qwen-audio-3.1-asr-flash-message` | Short voice messages | **16 kHz only** |
| `fun-asr-realtime` | Streaming baseline | Flexible |
| `paraformer-realtime-v2` | Lower-cost comparison | Flexible |
| `paraformer-realtime-8k-v2` | 8 kHz telephony | **8 kHz only** |

All five model IDs accept PCM, PCM WAV, MP3, Ogg Opus, Ogg Speex, AAC and AMR-NB;
`aliyun.SupportedAudio(model)` exposes the model-specific allowed format,
sample rate and mono-channel constraints. Ogg encapsulation is required for
Opus/Speex. See [audio format guide](../../../docs/AUDIO.md) for decoding and
re-sampling limitations.

The adapter uses a separate WebSocket connection per task. Connection pooling and
cross-task reuse are **not implemented yet**. Input chunks must already be encoded
to match `asr.Config.Format`; the adapter does not resample or transcode.

## Usage

```go
client, err := aliyun.New(aliyun.Config{
    APIKey: os.Getenv("DASHSCOPE_API_KEY"),
    Model: aliyun.ModelQwenAudio31Streaming,
})
if err != nil { return err }
s, err := client.OpenStream(ctx, asr.Config{
    Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
})
if err != nil { return err }
defer s.Close()
// Start a receiver concurrently before sending a long audio stream.
// s.Send(ctx, chunk) for each PCM chunk, then s.CloseSend(ctx).
// Drain s.Recv(ctx) until io.EOF; EventFinal confirms only a transcript segment.
```

For Qwen-Audio 3.1 Message, set `IntermediateResults: true` to receive partial
transcripts; it defaults to final results only. For children's conversations,
leave `DisfluencyRemoval` disabled by default to preserve the original speech.

The default endpoint is the China DashScope service. Set `Endpoint` to the corresponding regional or workspace-specific URL when needed. Keys stay on the backend.
Use `DASHSCOPE_API_KEY` from the environment; never commit credentials.

See [official realtime ASR guide](https://help.aliyun.com/zh/model-studio/real-time-speech-recognition-user-guide).

## Advanced request options

Set `asr.Config.Options.Provider` to `aliyun.Options` for request-local overrides of constructor settings, multiple language hints, supported VAD controls, and initial dialogue context. Model-specific validation rejects unsupported explicit flags. Qwen Streaming and Fun-ASR support sensitive-word filters; Paraformer supports semantic segmentation, punctuation and ITN. Qwen Message and Paraformer support disfluency removal. Qwen Message supports intermediate results and dynamic context via the optional aliyun.ContextUpdater interface. Initial and dynamic context are limited to five messages per role and 400 characters per turn; no SDK truncation or automatic context management.

Set `asr.Config.Options.UtteranceDetails` to obtain sentence/word timing via `Event.Utterance` or `Result.Utterances`. Native sentence IDs, word punctuation and stability flags are preserved. Paraformer 8k v2 may return emotion and confidence in final sentences with semantic segmentation disabled; no synthetic labels are generated. Live API validation remains a follow-up.

## Dynamic context and usage

For Qwen-Audio-ASR-Message, call `stream.(aliyun.ContextUpdater).UpdateContext(ctx, messages)` after OpenStream and before CloseSend. This sends a native continue-task command, serialized with audio writes. Other models return CodeUnsupported; invalid or empty context is rejected without consuming audio. A successful command write is not a server acknowledgment. The SDK does not append to, remember, or replay conversation history.

Read `stream.(asr.SessionMetadataReceiver).SessionMetadata()` during recognition or after EOF; `Recognize` also returns the metadata through `Result.Provider`. When present it is `aliyun.SessionDetails` with `Usage` containing optional duration in seconds and input/output/total tokens. The snapshot is cumulative, not an incremental count. Use Final=true to identify usage returned by task-finished. Missing counters are nil; the SDK does not estimate or add counters, or mark intermediate usage final when completion omits it. Malformed optional usage fields do not interrupt text recognition.

References: https://help.aliyun.com/zh/model-studio/qwen-asr-message-client-events and https://help.aliyun.com/zh/model-studio/qwen-asr-message-server-events and https://help.aliyun.com/zh/model-studio/qwen-audio-asr-streaming-server-events .
