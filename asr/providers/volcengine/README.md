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

## Regression coverage

The paid automated Volcengine run also checks a short synthesized English
"Hello." utterance (with a trailing silence interval). A short recognition
result is required, not merely a successful WebSocket handshake. The offline
mock tests stress four independent sessions on the same Client and a
per-receive timeout that must close only its own stream and keep its original
context error on later reads. These tests do not establish accuracy for
Mandarin, children, noise, or production-scale concurrency.

## Mandarin integration checks

The automatic billable Volcengine workflow additionally creates two Mandarin
PCM fixtures using the offline `espeak-ng -v zh` voice: a short "你好" greeting
and the sentence "今天天气很好". These are recognized in real time through
Seed-ASR 2.0 using its default language handling: `asr.Config.Language` must
remain unset on this endpoint because language selection is not supported.
The real cloud assertions require confirmed Chinese transcript content, not
just a successful handshake. The longer Mandarin sentence must match the
expected "天气" keyword in both response modes. The ultra-short synthetic
"你好" sample is a **transport smoke test only**: it must produce a confirmed
Chinese result, but a mismatch is logged as a quality diagnostic rather than
failing the entire SDK CI. Seed-ASR 2.0 has actually returned "离" for this
synthetic sample in an otherwise healthy run, while previous runs returned
"你好". This variability is an unresolved short-speech accuracy concern,
not a claimed fix. These machine-generated samples do **not** validate
recorded children, accents, overlapping voices, or background noise.

## Continuous-session regression

The billable live workflow streams four Mandarin utterances over **one**
WebSocket, separated by two-second silences, in both `ResultFull` and
`ResultSingle` modes. The test repeats two complete phrases ("今天天气很好" and "我们一起玩游戏")
on alternating turns, verifying that a new utterance is **not** mistaken for
an old cumulative snapshot. It checks four confirmed sentence events, correct
order and counts, and stable EOF. An offline test replays 32 cumulative
snapshots. Earlier exploratory runs of short synthetic utterances in a
continuous session had recognition failures: "明天见" was heard as "民间片",
and later "你好" was not confirmed at the start while the last "你好" became
"李敖" in both response modes. These are real **unresolved ASR/fixture
quality observations**; neither the adapter nor the test can guarantee
the accuracy of very short utterances within a continuous conversation.
This test is not a multi-hour soak test or a children's-voice benchmark.

## Bounded duration and concurrency regression

The automatic billable Volcengine workflow includes a separate one-connection
Mandarin test with **at least 60 seconds of PCM**, 12 alternating full
utterances and 4-second silent gaps. It requires 12 ordered confirmed results,
six occurrences each of the two keywords, and stable EOF. It logs partial
count and time to first confirmed result, without imposing arbitrary accuracy
or latency targets. The offline local WebSocket test concurrently runs **32
independent sessions** on one shared Client, checking unique request IDs,
per-session final transcript isolation and terminal EOF under `go test -race`.
Neither test is a multi-hour soak nor a real-provider 32-connection load test,
and neither validates recorded children's speech.

## Real Ogg Opus transport and session teardown

The paid Volcengine CI also verifies a **real** Ogg Opus session, encoding
a deterministic Mandarin PCM fixture with FFmpeg's `libopus`. The test sends
complete Ogg pages of one uninterrupted logical stream, paced using the
48 kHz Opus granule-position timeline. The ASR request explicitly sets
`Format{Encoding: audio.EncodingOpus, SampleRate: 16000, Channels: 1}`,
which maps to `audio.format="ogg"` and `audio.codec="opus"`; the SDK does not
implicitly encode, decode, remux, or adjust the audio. Real Seed-ASR 2.0 must
produce a confirmed Chinese transcript containing "天气" and clean EOF.

The offline race suite also checks that `Close` releases a connection even
without calling `Recv`, and canceling one active stream does not terminate
another stream sharing the same Client. These are scoped resource-release
regressions, **not** proof of a leak-free multi-hour production deployment.
Real microphone recordings and embedded-device Ogg muxers remain unverified.

### Ultra-short synthetic speech CI diagnostics

The existing real `TestVolcengineLiveShortSpeech` still **fails** when the
synthesized English "Hello." has no confirmed recognition or wrong text. In
practice the same real service has sometimes returned `[]` despite
previously recognizing `hello`. This is a **known unresolved short-speech
quality risk**, not a proven Ogg/PCM SDK framing defect.

The automatic workflow runs this *unchanged strict test* in its own
`continue-on-error` diagnostic step and produces an explicit GitHub Actions
warning / job summary when it fails. The separate protocol and long-duration
cloud checks remain **gating**: they must pass in order for the workflow to
succeed. A green workflow therefore **does not** claim that ultra-short speech
is reliably recognized. Real recorded voice datasets are required for
acoustic-quality acceptance before production deployment.

## Live continuous Ogg Opus incremental results

The billable GitHub Actions flow now also encodes the four-sentence Mandarin
conversation as **one** valid Ogg Opus logical stream, with complete Ogg pages
sent in granule-time order on one WebSocket, in `ResultSingle` and `ResultFull`.
The test records first provisional and confirmed result arrival, confirms at
least one **EventFinal arrives before CloseSend** (audio still streaming),
and checks that repeated sentence text is not dropped or duplicated.
`EventPartial` count is reported independently: partial hypotheses can be
absent for some model/voice samples and therefore a zero count is **not**
interpreted as an SDK failure. No production p95 latency or real-device
voice claim follows from these synthetic fixtures.

An offline unit test verifies that test-harness Ogg page splitting preserves
exact bytes, granule positions, and rejects incomplete or malformed pages.
This is a test helper, not a new SDK transcoding/remuxing path.
