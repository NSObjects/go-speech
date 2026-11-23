# Audio handling in go-speech

This package is intentionally separate from ASR, TTS and Eino. It describes
**actual** audio bytes, not a requested format. No implicit conversion takes
place when calling an ASR Provider.

## Format representation

| Encoding | Meaning | Transcoding in the core library |
| --- | --- | --- |
| `pcm_s16le` | Raw signed 16-bit little-endian PCM, no file header | Yes |
| `wav` | RIFF/WAVE file, **uncompressed 16-bit PCM only** | Yes |
| `mp3` | MP3 with ID3 or MPEG frame headers | No |
| `opus` | **Ogg Opus**, not raw Opus frames | No |
| `speex` | **Ogg Speex**, not raw Speex frames | No |
| `aac` | AAC with an ADTS header (for `Detect`) | No |
| `amr` | **AMR-NB**, not AMR-WB | No |

`Detect(data)` checks recognizable signatures, not the full validity of
compressed streams. It **cannot** infer the sample rate of headerless PCM.
`ValidateBuffer` works only on **complete** input buffers; do not call it on
an arbitrary MP3, Ogg, or WAV streaming chunk.

`DecodeWAVPCM16` reads conventional PCM16 RIFF/WAVE, including extra RIFF
chunks, and rejects floating-point or compressed WAV. `EncodeWAVPCM16`
creates a standard RIFF/WAVE header.

## Model capabilities

`audio.Capability` declares the encodings, sampling rates, and channel counts
accepted by a provider/model. For example,
`aliyun.SupportedAudio(aliyun.ModelQwenAudio31Message)` requires 16 kHz mono,
while `aliyun.ModelParaformerRealtime8K` requires 8 kHz mono.

All five currently supported Alibaba Cloud models accept the seven encoding
identifiers above, **but the encoded bytes must actually match the identifier**.
Ogg is mandatory for Opus/Speex; WAV must contain PCM; AMR is AMR-NB.

`SupportedAudio` validates **metadata**, not the content of arbitrary
streaming chunks. If the upstream device sends raw Opus packets, do **not**
declare those bytes as `audio.EncodingOpus`; repackage or decode them first.

## Convert complete PCM16 audio

```go
source := audio.Buffer{
    Data: micData, // PCM16LE, 48 kHz, stereo
    Format: audio.Format{
        Encoding: audio.EncodingPCM16LE, SampleRate: 48000, Channels: 2,
    },
}
target := audio.Format{
    Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1,
}
converted, err := audio.ConvertPCM16(source, target)
// converted.Data is 16 kHz mono PCM16LE.
```

`ConvertPCM16` also reads and writes PCM16 WAV files. A windowed-sinc FIR
low-pass resampler suppresses aliasing when downsampling.

## Convert live PCM16 audio

For streaming ASR, **never** independently resample each incoming packet:
fractional resampler state must persist between chunks.

```go
converter, err := audio.NewPCM16StreamConverter(
    audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 48000, Channels: 2},
    audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
)
if err != nil { return err }

for _, devicePacket := range incomingPackets {
    pcm, err := converter.Process(devicePacket)
    if err != nil { return err }
    if len(pcm) != 0 {
        if err := session.Send(ctx, pcm); err != nil { return err }
    }
}
tail, err := converter.Flush()
if err != nil { return err }
if len(tail) != 0 {
    if err := session.Send(ctx, tail); err != nil { return err }
}
return session.CloseSend(ctx)
```

The streaming converter is **not safe for concurrent Process/Flush calls**.
`Flush` is necessary: it checks incomplete PCM frames and emits buffered
filter tail. The converter intentionally has a short filter lookahead.

## Optional FFmpeg backend for complete compressed audio

The independent package `audio/ffmpeg` adds opt-in decoding and resampling of
**complete buffers** (MP3, Ogg Opus, Ogg Speex, AAC, AMR-NB, PCM16, WAV) to
PCM16LE or PCM16 WAV using an installed `ffmpeg` binary. It does not add CGo,
native codec libraries, or an FFmpeg requirement to the core `audio` package.

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
result, err := audio.Convert(ctx, compressed, audio.Format{
    Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1,
}, ffmpeg.Converter{MaxInputBytes: 8 << 20, MaxOutputBytes: 16 << 20})
```

Import `context`, `github.com/NSObjects/go-speech/audio`, and
`github.com/NSObjects/go-speech/audio/ffmpeg` as appropriate.

`audio.Convert` is a format-aware router: it copies matching complete input,
uses built-in PCM/WAV conversion where possible, and delegates compressed
transcoding to the explicit backend. It rejects mismatched output metadata.
The FFmpeg adapter uses stdin/stdout pipes, caps input and decoded output,
observes context cancellation, and limits stderr. Callers should separately
apply timeouts, concurrency limits and duration limits for untrusted input.

**Complete-buffer Converter is not a streaming decoder.** Use the separate
persistent `OpenStreamDecoder` for a continuous compressed stream. Never start
one process per microphone packet. If the ASR model accepts the original
compressed encoding, prefer direct passthrough without decoding.

## Zero-transcode Opus: passthrough or pure-Go Ogg encapsulation

Alibaba Cloud ASR accepts **Ogg Opus** input, not bare raw Opus packets.
A decoded PCM intermediate is unnecessary when the target model supports
Opus. Select the cheapest compatible representation:

1. **Already Ogg Opus:** send the original continuous Ogg bytes as `audio.EncodingOpus` without changes.
2. **Complete raw Opus packets:** use `opus.OggMuxer` to add Ogg pages,
   keeping the original compressed packet bytes unchanged. The muxer does
   not use CGo or FFmpeg.
3. **Unsupported codec/model:** only then use the optional decoder to PCM.

For one raw Opus capture session:

```go
// Imports: github.com/NSObjects/go-speech/asr,
// github.com/NSObjects/go-speech/audio,
// github.com/NSObjects/go-speech/audio/codec/opus.
muxer, err := opus.NewOggMuxer(opus.OggConfig{
    Channels: 1, InputSampleRate: 16000, PreSkip: 0, // Replace 0 with the encoder's actual lookahead (48 kHz samples) if known.
})
if err != nil { return err }

// The session uses audio.Format{Encoding: audio.EncodingOpus,
// SampleRate: 16000, Channels: 1} and a model supporting Ogg Opus.
headers, err := muxer.Headers()
if err != nil { return err }
if err := session.Send(ctx, headers); err != nil { return err }
for _, rawOpusPacket := range packets {
    page, err := muxer.WritePacket(rawOpusPacket) // one *complete* packet
    if err != nil { return err }
    if err := session.Send(ctx, page); err != nil { return err }
}
eos, err := muxer.Close()
if err != nil { return err }
if err := session.Send(ctx, eos); err != nil { return err }
return session.CloseSend(ctx)
```

The muxer generates both mandatory Ogg headers, uses random logical stream
serials, page sequence numbers, RFC 6716 packet-duration parsing, granule
positions measured at 48 kHz, and Ogg CRCs. Each packet yields one page
with no extra packet buffering. `Close` generates the EOS page.

**Encoder lookahead matters:** `PreSkip` is supplied by the encoding device,
measured in 48kHz samples. It is not discoverable from the raw packet bytes
alone. Using zero when unknown may expose a few samples of decoder priming;
do not invent an encoder-specific value. For pre-encoded Ogg, preserve the
original `OpusHead` and skip conversion entirely.

Do not pass RTP headers, corrupted/lost/reordered packets, or Opus packet
fragments to `WritePacket`. This is **not** an RTP depacketizer, packet-loss
concealment engine, or jitter buffer. Each microphone conversation must use
a new Ogg muxer. A raw packet plus an added Ogg header is not the same thing
as encoding PCM again.

Live CI tests both **Ogg Opus direct passthrough** and **raw Opus → pure-Go
Ogg muxer → ASR**, with no Opus decoding. The separate FFmpeg/libopus
to-PCM tests remain as compatibility fallbacks.

## Persistent compressed stream decoder

The optional `audio/ffmpeg` package now also provides
`ffmpeg.OpenStreamDecoder(ctx, StreamConfig)`. **One subprocess persists
throughout the stream**; incoming byte fragments share codec and container
state. Its input may be continuous **Ogg Opus**, Ogg Speex, AMR-NB, MP3,
ADTS AAC, PCM16LE or WAV bytes. Output is raw PCM16LE in the requested
sample rate and channel count. This is separate from the complete-buffer
`audio.Convert` API.

```go
decoder, err := ffmpeg.OpenStreamDecoder(ctx, ffmpeg.StreamConfig{
    Input: audio.Format{
        Encoding: audio.EncodingOpus, SampleRate: 48000, Channels: 1,
    },
    Output: audio.Format{
        Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1,
    },
})
if err != nil { return err }
defer decoder.Close()

// Two concurrent loops are required to avoid pipe deadlock:
// A producer calls decoder.Write(ctx, contiguousOggBytes) for every chunk,
// then decoder.CloseInput(). A consumer calls decoder.Read(ctx, buffer)
// until io.EOF, forwarding returned PCM bytes.
```

The decoder supports a single concurrent writer and a single concurrent
reader. `CloseInput` is idempotent and allows the decoder's buffered audio
to flush; `Close` aborts and reaps the process. Canceling an I/O operation
context ends the entire decoder session. Output read chunk boundaries
are arbitrary: a Read may split a PCM sample.

**Crucial format distinction:** A raw packet produced by a device using
the Opus codec is *not* an Ogg container. This FFmpeg stream implementation
accepts **Ogg Opus pages**, not bare Opus packets. Device packet formats
require explicit encapsulation or a separate codec implementation.
AMR-NB accepts its single `#!AMR` header followed by consecutive frames.
Each stream must start with its own header, and the header must not be
repeated for every chunk.

FFmpeg demuxer buffering and codec lookahead can delay PCM availability,
so a working persistent process is not automatically a low-latency
production solution. Measure time-to-first-PCM and worst-case latency
using real device audio. Prefer direct compressed passthrough if the
selected ASR model accepts it, and consider a native in-process decoder
for bare Opus/AMR frames or strict latency targets.

## Compressed audio and performance

- If the destination accepts the original encoding and sample rate, **pass
  the bytes through**; do not decode/re-encode without a reason.
- Core `go-speech/audio` does **not** decode MP3, Ogg Opus, Ogg Speex, AAC or
  AMR. `ConvertPCM16` returns `audio.ErrUnsupportedConversion`; the opt-in
  `audio/ffmpeg` backend adds complete-buffer decoding only.
- The FFmpeg executable is an external runtime dependency of the optional
  batch and persistent stream adapters, not a Go module dependency.
  A separate subprocess *per packet* would be inappropriate.
- Audio conversion is **explicit**. ASR `Stream.Send` transmits exactly the
  bytes supplied by the caller.
- This package does not implement VAD or device audio playback.

## Test

```sh
go test -race ./audio ./asr/providers/aliyun
```

## Native raw Opus / AMR-NB packet decoders (optional CGo)

Device transports may send **raw Opus codec packets** or **AMR-NB storage
frames** rather than Ogg/AMR file streams. Do **not** label a raw Opus
packet as `audio.EncodingOpus`: that encoding represents **Ogg Opus** in
the existing file/stream ASR interface.

New optional codec modules:

- `audio/codec/opus`: one stateful decoder per raw-Opus stream; call
  `Decode(packet)` for each complete packet. Sample rates 8/12/16/24/48kHz,
  mono/stereo, returns PCM16LE. **Requires libopus when enabled**.
- `audio/codec/amrnb`: one stateful decoder per AMR-NB frame stream, each
  20ms frame decoded to **320 bytes of 8kHz mono PCM16LE**. Expects one
  3GPP file-storage frame: **one-byte FT/Q header + speech bytes**, not
  an RTP payload or a complete `#!AMR` file. **Requires OpenCORE-AMR-NB
  when enabled**. A pure-Go `ValidateFrame` rejects reserved FT or bad
  frame lengths.

Enable each optional native module at build time:

```sh
# Ubuntu/Debian prerequisites:
sudo apt-get install -y pkg-config libopus-dev libopencore-amrnb-dev

CGO_ENABLED=1 go test -tags="libopus opencoreamr" ./...
# Build applications with those tags to enable native packet decoders.
```

On macOS, install `opus` and `opencore-amr` via Homebrew if available
for the target platform, and ensure `pkg-config` can find the package.
Distributions must separately package the system codec libraries.

Example: raw Opus packet → PCM16LE using libopus:

```go
decoder, err := opus.NewDecoder(16000, 1)
if err != nil { return err }
defer decoder.Close()

// packet is one complete raw Opus payload, not an Ogg page.
pcm, err := decoder.Decode(packet)
if err != nil { return err }
_ = pcm  // ready for the selected model's PCM input
```

If the application is built without optional tags, calling `NewDecoder`
returns `opus.ErrUnavailable` or `amrnb.ErrUnavailable`. The SDK
never silently enables a platform-specific dependency.

### Pure-Go AMR-NB file framing for the FFmpeg backend

For devices that send successive **AMR-NB storage frames** with one-byte
FT/Q headers but no initial file magic, `amrnb.Framer` emits `#!AMR\n`
only once, followed by validated frames, into the existing long-lived
`ffmpeg.StreamDecoder`. It is useful when a native AMR decoder cannot
be installed, while keeping the subprocess state across packets.

```go
stream, err := ffmpeg.OpenStreamDecoder(ctx, ffmpeg.StreamConfig{
    Input: audio.Format{Encoding: audio.EncodingAMR, SampleRate: 8000, Channels: 1},
    Output: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
})
if err != nil { return err }
defer stream.Close()

framer, err := amrnb.NewFramer(stream)
if err != nil { return err }
// Run a concurrent stream.Read(ctx, pcmBuf) loop to avoid pipe backpressure.
for _, frame := range amrFrames {
    if err := framer.WriteFrame(ctx, frame); err != nil { return err }
}
return framer.CloseInput()
```

**RTP is a distinct framing layer.** RFC 4867 packets may contain CMR,
ToC lists, multiple speech frames, and bandwidth-efficient bit packing.
Do not pass complete RTP payloads directly to these per-frame decoders.
Parse and depacketize the transport first. Likewise, AMR-WB is not AMR-NB.

These decoders have their own per-session codec state. For raw Opus, libopus
supports at most 120ms audio per encoded packet. Both native decode methods
return allocated, caller-owned PCM bytes; reuse a decoder across consecutive
packets of one call and close it at end of session.
