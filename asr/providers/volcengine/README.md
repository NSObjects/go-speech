# Volcengine Doubao Streaming ASR

Implementation of the **official large-model streaming speech recognition V3**
optimized bidirectional WebSocket interface (`bigmodel_async`).

- Official reference: https://docs.volcengine.com/docs/doubao-voice/large-model-streaming-automatic-speech-recognition-api-1?lang=zh
- Recommended endpoint: `wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async`
- Recommended 2.0 hourly resource: `volc.seedasr.sauc.duration` (must be enabled on the API key).
- Other 2.0 resource: `volc.seedasr.sauc.concurrent`.
- Auth: new console `X-Api-Key` **or** legacy ASR `X-Api-App-Key` (App ID) + `X-Api-Access-Key` (Access Token), plus `X-Api-Resource-Id` and random request/connection IDs.
- Wire format: Volcengine V3 binary WebSocket frames, gzip JSON config and gzip audio frames.
- Supported input: 16 kHz PCM16LE, PCM16 WAV, MP3, **Ogg Opus**; no automatic conversion.
- This package implements `asr.StreamRecognizer`, not batch ASR or TTS.

## Usage

```go
import (
    "github.com/NSObjects/go-speech/asr"
    "github.com/NSObjects/go-speech/audio"
    "github.com/NSObjects/go-speech/asr/providers/volcengine"
)

client, err := volcengine.New(volcengine.Config{
    APIKey: apiKey, // Or use AppID + AccessToken for legacy ASR auth.
    ResourceID: volcengine.ResourceSeedASR2,
    EnableNonStream: true, // optional second pass for accurate sentence finals
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

`EventPartial` is a changeable hypothesis. `EventFinal` is a confirmed
utterance, *not necessarily a conversation turn*. `CloseSend` ends the audio
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

To run the **real online** integration test, configure
`VOLCENGINE_API_KEY`, optionally `VOLCENGINE_RESOURCE_ID`, and run the
repository's `Volcengine ASR Integration` workflow. The workflow runs on
pushes automatically when the API key secret has been configured; it skips
the live request when no credentials are available. Real cloud recognition
requires the matching resource enabled in the Volcengine console.

The real endpoint is **not verified until a live job succeeds**. Local
mock-server tests do not prove the upstream API has accepted a request.

### Old-console ASR authentication

The **streaming ASR** `bigmodel_async` endpoint supports the documented legacy pair:

```go
client, err := volcengine.New(volcengine.Config{
    AppID: appID,
    AccessToken: accessToken,
    ResourceID: volcengine.ResourceSeedASR2,
})
```

For GitHub Actions, configure `VOLCENGINE_APP_ID` as a repository variable
(or secret) and `VOLCENGINE_ACCESS_TOKEN` as a repository secret. The
workflow uses this pair when both are present, otherwise it uses
`VOLCENGINE_API_KEY`. It **fails** when credentials are missing or only
one member of the legacy pair is present. Do not put tokens in repository
variables, commits or workflow logs.

**Important:** The official ASR V3 legacy headers are `X-Api-App-Key` and
`X-Api-Access-Key`. The names `X-Api-App-Id` and `X-Api-App-Token` are
**not** the documented legacy pair for this ASR endpoint. Do not assume an
App Token issued for another voice service is accepted as an ASR Access Token.
Official reference: https://docs.volcengine.com/docs/doubao-voice/old-version-console-authentication-reference-example?lang=zh
