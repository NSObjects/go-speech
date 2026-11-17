# Volcengine Doubao Streaming ASR

Implementation of the **official large-model streaming speech recognition V3**
optimized bidirectional WebSocket interface (`bigmodel_async`).

- Official reference: https://docs.volcengine.com/docs/doubao-voice/large-model-streaming-automatic-speech-recognition-api-1?lang=zh
- Recommended endpoint: `wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async`
- Recommended 2.0 hourly resource: `volc.seedasr.sauc.duration` (must be enabled on the API key).
- Other 2.0 resource: `volc.seedasr.sauc.concurrent`.
- Auth: `X-Api-Key`, `X-Api-Resource-Id`, random request/connection IDs.
- Wire format: Volcengine V3 binary WebSocket frames, gzip JSON config and gzip audio frames.
- Supported input: 16 kHz PCM16LE, PCM16 WAV, MP3, **Ogg Opus**; no automatic conversion.
- This package implements `asr.StreamRecognizer`, not batch ASR or TTS.

## Usage

```go
import (
    "time"

    "github.com/NSObjects/go-speech/asr"
    "github.com/NSObjects/go-speech/audio"
    "github.com/NSObjects/go-speech/asr/providers/volcengine"
)

client, err := volcengine.New(volcengine.Config{
    APIKey: apiKey,
    ResourceID: volcengine.ResourceSeedASR2,
    EnableNonStream: true, // optional second pass for accurate sentence finals
    EndWindowSize: 700 * time.Millisecond, // optional provider-side silence threshold
})
if err != nil { return err }

stream, err := client.OpenStream(ctx, asr.Config{
    Format: audio.Format{
        Encoding: audio.EncodingOpus,
        SampleRate: 16000,
        Channels: 1,
    },
})
if err != nil { return err }
defer stream.Close()

// From separate sender and receiver goroutines:
//   stream.Send(ctx, oggOpusChunk) for every consecutive Ogg Opus chunk
//   stream.CloseSend(ctx) after last chunk
//   stream.Recv(ctx) until io.EOF
```

A raw Opus packet is **not** an Ogg Opus stream. Use the optional pure-Go
`audio/codec/opus.OggMuxer` if you need to encapsulate raw packets before
passing them to the provider; the provider never transforms input implicitly.

## Streaming semantics

For real-time dialogue, choose the provider's `ResultType` in `Config`:

- `ResultFull` (default): cumulative transcript snapshots, with repeated
  confirmed utterances deduplicated by the adapter.
- `ResultSingle`: current-utterance updates only. This is generally easier
  to consume for incremental user speech display and AI toy conversations.

With `show_utterances` enabled, `EventPartial` is the latest hypothesis for
the **current unconfirmed utterance**, not the cumulative full text.
`EventFinal` is a confirmed sentence segment, not necessarily a conversation
turn. Duplicate confirmed sentences from repeated full responses are not
emitted twice. When the last packet has no utterance metadata, the adapter
also preserves any trailing unconfirmed text as a final segment.

`EndWindowSize` optionally configures Volcengine's own `end_window_size` silence
threshold for returning `definite` sentences (milliseconds on the wire, minimum
200ms; omit to use the service default of approximately 800ms). For example,
`700*time.Millisecond` adjusts provider-side segmentation without changing the
application's VAD or when it ends a conversation turn. It is not a local
endpoint detector and does not automatically call `CloseSend`.

The SDK does not decide how to stop VAD or when to call LLM/TTS; that remains
the application's responsibility. `CloseSend` ends the audio
input with the protocol's final binary message and keeps result reception
open. `Recv` finishes only after the provider's last response, preserving
stable `io.EOF` or the first terminal error.

The provider exposes standard `speecherr.Error` categories and original
provider error codes. Network, protocol, and authorization failures are
covered by local mock-WebSocket tests. It does not silently retry or perform
midstream provider failover.

Only the optimized bidirectional V3 endpoint is implemented. The
`bigmodel_nostream` one-sentence endpoint and Volcengine Agent Plan
`/api/v3/plan/...` routes are separate service modes, not interchangeable
endpoint strings.

## Tests

Run offline checks:

```bash
go test -race ./asr/providers/volcengine
go vet ./...
```

## New-console authentication only

The official Seed-ASR 2.0 optimized streaming endpoint uses:

```http
X-Api-Key: <API key issued in the new Doubao Speech console>
X-Api-Resource-Id: volc.seedasr.sauc.duration
```

The SDK intentionally **does not support old-console AppID / Access Token
authentication**. The new API key is not an AppID, an App Token for another
voice service, an Ark model API key, or an Alibaba credential.

Configure these GitHub Actions settings for the `NSObjects/go-speech`
repository:

- **Repository secret** `VOLCENGINE_API_KEY`: the actual API Key (called
  APP Key in some official API docs) from the **new Doubao Voice console →
  API Management** page.
- **Repository variable** `VOLCENGINE_RESOURCE_ID`: optional; defaults to
  `volc.seedasr.sauc.duration` for **Seed-ASR 2.0 hourly/duration** billing.
  Use `volc.seedasr.sauc.concurrent` only if that concurrency service
  has been enabled for the same project.
- **Repository variable** `VOLCENGINE_WS_ENDPOINT`: optional; leave unset
  to use the official `bigmodel_async` URL.

The automatic paid integration workflow fails explicitly if the API key is
missing, and tests real cloud recognition when configured. A green Go CI
alone does not prove upstream authentication is working.

### If a live run returns HTTP 401

The WebSocket handshake was rejected **before any audio was sent**.
Confirm that:

1. The key comes from the **new Doubao Voice console's API management**,
   rather than from an old AppID/AppToken or another product's console.
2. The API key is enabled and belongs to the **same selected project** as
   the ASR model entitlement.
3. Under **Activation Management**, Seed-ASR streaming 2.0 is activated
   in that project, with the same duration or concurrency resource mode.
4. You replaced the **Repository secret**, not a similarly named variable,
   and the secret has no accidental leading/trailing whitespace.

The SDK records HTTP status and the service's `X-Tt-Logid` to help trace
handshake failures without exposing credentials or response bodies. If the
key and resources are correct, share the log ID with Volcengine support.

- [Official streaming ASR API](https://docs.volcengine.com/docs/doubao-voice/bidirectional-streaming-automatic-speech-recognition-websocket?lang=zh)
- [New console guidance](https://docs.volcengine.com/docs/doubao-voice/console-guidance?lang=zh)

The paid live GitHub Actions integration exercises real synthesized audio on
Seed-ASR 2.0, including two different spoken utterances separated by two seconds
of silence. Both `ResultFull` and `ResultSingle` must return ordered finalized
text for each utterance without repeating the earlier transcript. Local
mock-WebSocket tests validate streaming and terminal error semantics. This
fixture checks multi-utterance correctness, not production WER, noisy speech,
accent coverage, or interruption latency.
