package audio

import (
    "bytes"
    "errors"
    "fmt"
)

// ErrUnknownEncoding means bytes do not have a recognizable container/codec
// signature. Headerless PCM16LE is never auto-detected; its metadata must be
// provided by the audio source.
var ErrUnknownEncoding = errors.New("audio: unknown encoding")

// ErrInvalidData means the declared encoded audio is malformed or mismatched.
var ErrInvalidData = errors.New("audio: invalid audio data")

// Detect identifies only formats with reliable signatures. It does not decode
// audio or infer sample rate. Opus/Speex require Ogg headers; AMR means AMR-NB.
// It deliberately never guesses raw PCM from arbitrary bytes.
func Detect(data []byte) (Encoding, error) {
    switch {
    case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) &&
        bytes.Equal(data[8:12], []byte("WAVE")):
        return EncodingWAV, nil
    case len(data) >= 6 && bytes.Equal(data[:6], []byte("#!AMR\n")):
        return EncodingAMR, nil
    case len(data) >= 3 && bytes.Equal(data[:3], []byte("ID3")):
        return EncodingMP3, nil
    case len(data) >= 7 && data[0] == 0xff &&
        data[1]&0xf0 == 0xf0 && data[1]&0x06 == 0:
        return EncodingAAC, nil // AAC with an ADTS header.
    case len(data) >= 3 && data[0] == 0xff && data[1]&0xe0 == 0xe0 &&
        data[1]&0x18 != 0x08 && data[1]&0x06 != 0 && data[2]&0xf0 != 0xf0:
        return EncodingMP3, nil
    case len(data) >= 27 && bytes.Equal(data[:4], []byte("OggS")):
        if data[4] != 0 {
            return "", fmt.Errorf("%w: unsupported Ogg version", ErrInvalidData)
        }
        segCount := int(data[26])
        if len(data) < 27+segCount {
            return "", fmt.Errorf("%w: incomplete Ogg segment table", ErrInvalidData)
        }
        start := 27+segCount
        switch {
        case len(data) >= start+8 && bytes.Equal(data[start:start+8], []byte("OpusHead")):
            return EncodingOpus, nil
        case len(data) >= start+8 && bytes.Equal(data[start:start+8], []byte("Speex   ")):
            return EncodingSpeex, nil
        default:
            return "", fmt.Errorf("%w: Ogg codec not identified", ErrUnknownEncoding)
        }
    default:
        return "", ErrUnknownEncoding
    }
}

// ValidateBuffer performs structural checks for a *complete* encoded buffer.
// It cannot verify speech content or fully decode compressed formats. Do not
// call it on arbitrary streaming chunks that may start in the middle of a frame.
func ValidateBuffer(b Buffer) error {
    if err := b.Format.Validate(); err != nil {
        return err
    }
    if len(b.Data) == 0 {
        return fmt.Errorf("%w: empty audio buffer", ErrInvalidData)
    }
    if b.Format.Encoding == EncodingPCM16LE {
        if len(b.Data) % (2*b.Format.Channels) != 0 {
            return fmt.Errorf("%w: incomplete PCM frame", ErrInvalidData)
        }
        return nil
    }
    if b.Format.Encoding == EncodingWAV {
        decoded, err := DecodeWAVPCM16(b.Data)
        if err != nil { return err }
        if decoded.Format.SampleRate != b.Format.SampleRate ||
            decoded.Format.Channels != b.Format.Channels {
            return fmt.Errorf("%w: WAV header and Format disagree", ErrInvalidData)
        }
        return nil
    }
    detected, err := Detect(b.Data)
    if err != nil { return err }
    if detected != b.Format.Encoding {
        return fmt.Errorf("%w: declared %s but detected %s", ErrInvalidData, b.Format.Encoding, detected)
    }
    return nil
}
