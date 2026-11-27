# Volcengine Seed-TTS 3.0

Supports complete-text HTTP JSONL streaming and incremental-text WebSocket synthesis. Both use the same SDK audio and stream contracts. Configure the API key and an authorized Seed-TTS 3.0 voice ID; this adapter never registers voices or selects a preset automatically.

```go
client, err := speech.NewTTS(speech.VolcengineTTS{
    APIKey: apiKey,
})
```

Use `Synthesize`, `SynthesizeStream`, or `speech.NewDuplexTTS`. Set the voice on the request. MP3 and PCM support 8–48 kHz at the documented rates; Ogg Opus requires 48 kHz mono. No transcoding.

The duplex stream accepts fragments through SendText. An optional `Flush(context.Context) error` method submits a batch without closing input. CloseSend submits remaining text and Recv returns EOF only after the provider's CLOSED final event. Close cancels the network session.

Official references: [HTTP streaming](https://docs.volcengine.com/docs/doubao-voice/seed-tts-3-0-http-streaming?lang=zh), [WebSocket](https://docs.volcengine.com/docs/doubao-voice/seed-tts-3-0-websocket?lang=zh).

Migration: the previous ResourceID configuration field is removed; use optional ModelID (defaults to doubao-seed-tts-3.0). Endpoint is now an HTTPS origin. Existing 2.0 voice IDs must be replaced with authorized 3.0 voice IDs.

## Text formats

Set `tts.Request.TextFormat` to `tts.TextSSML` for complete SSML input via HTTP, or `tts.TextInlineTag` for native inline tags. The latter is also accepted by `tts.DuplexRequest` on WebSocket. SSML is rejected in duplex mode. The SDK passes inline tags unchanged; use only tags documented for the selected mode and keep each tag in one fragment until split-tag behavior is verified. Actual expression and SSML tag support depend on the model and voice.

## Advanced request options

Use `tts.Options{Provider: volcengine.Options{...}}` on either request type. Native options include an ordered instruction list, language override, pronunciation/replacement rules, text normalization, Markdown filtering, watermark configuration and output bit rate in bps. `SessionID` is caller-owned: reuse only for serial requests in the same conversation, not unrelated or concurrent calls. Server context lifetime is not guaranteed. HTTP-only `SilenceDurationMS` appends silence at the end; duplex rejects it. Do not mix the native instruction list with the common `Instruction` field. Optional boolean pointers preserve explicit false.

## Alignment events

Set `volcengine.Options.EnableSubtitle` to true on a streaming or duplex request. Assert the returned stream as `tts.EventReceiver` and call `RecvEvent(ctx)` instead of `Recv(ctx)`. Audio, alignment and Flush completion events share a single receiver and keep their arrival order. Alignment may arrive after a Flush completion event. Drain until CLOSED/EOF. `Recv` remains audio-only; mixing the two receive methods returns `tts.ErrReceiveMode` without consuming data. For complete text with subtitles, use `tts.SynthesizeDetailed(ctx, client, req)` or `SynthesizeStream` with `RecvEvent`; `Synthesize` returns audio only and rejects explicit subtitle enablement. Provider phoneme objects are preserved as raw JSON in `volcengine.AlignmentDetails` because individual phoneme schemas are not documented. Language/model support and live timing still require verification.

## Native flow control

Duplex streams implement `tts.Flusher`. Flush submits a batch without closing input; `EventBatchCompleted` is observable in event mode. For MP3, each flushed batch has its own file header. Use `RecvEvent` and separate the audio bytes at `EventBatchCompleted`; do not treat the concatenated batches as one encoded file. Alignment events can arrive after the batch's audio boundary. The documented interrupt command is not exposed yet because the protocol does not define the subsequent session lifecycle. Use Close for local termination; the SDK does not emulate interruption or reopen sessions.
