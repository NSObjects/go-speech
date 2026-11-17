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
