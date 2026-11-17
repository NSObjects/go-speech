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

## Compressed audio and performance

- If the destination accepts the original encoding and sample rate, **pass
  the bytes through**; do not decode/re-encode without a reason.
- `go-speech/audio` does **not** currently decode or transcode MP3, Ogg Opus,
  Ogg Speex, AAC or AMR. `ConvertPCM16` returns
  `audio.ErrUnsupportedConversion` for these formats.
- Codec backends may be added separately; a per-chunk FFmpeg subprocess would
  be inappropriate for low-latency streaming.
- Audio conversion is **explicit**. ASR `Stream.Send` transmits exactly the
  bytes supplied by the caller.
- This package does not implement VAD or device audio playback.

## Test

```sh
go test -race ./audio ./asr/providers/aliyun
```
