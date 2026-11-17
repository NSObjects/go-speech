package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// ErrUnsupportedConversion means that a codec/format requires an external
// decoder/encoder. The standard library implementation only handles PCM16LE
// and PCM16-in-WAV.
var ErrUnsupportedConversion = errors.New("audio: unsupported conversion")

// PCM16StreamConverter converts signed little-endian PCM audio across 8/16/48
// kHz (and other positive rates), including mono/stereo channel conversion.
// It maintains filter history and fractional phase across Process calls: chunk
// boundaries do not restart the resampler. Calls must not be concurrent.
//
// A windowed-sinc low-pass filter is used for rate changes to suppress aliasing.
// It buffers a small lookahead (approximately 16 input frames); call Flush at
// end of input to emit the remaining samples.
type PCM16StreamConverter struct {
	input, output  Format
	pending        []byte
	samples        []float64 // interleaved output-channel samples, after channel mapping
	base, received int64     // global input frame range [base, received)
	next           int64     // index of next output frame
	finished       bool
}

const sincRadius = 16

func NewPCM16StreamConverter(input, output Format) (*PCM16StreamConverter, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	if err := output.Validate(); err != nil {
		return nil, err
	}
	if input.Encoding != EncodingPCM16LE || output.Encoding != EncodingPCM16LE {
		return nil, fmt.Errorf("%w: streaming converter requires pcm_s16le", ErrUnsupportedConversion)
	}
	if input.Channels > 2 || output.Channels > 2 {
		return nil, fmt.Errorf("%w: only mono and stereo supported", ErrUnsupportedConversion)
	}
	return &PCM16StreamConverter{input: input, output: output}, nil
}

// Process accepts arbitrary byte boundaries, even a partial PCM16 frame.
// No output is guaranteed until there is sufficient input for the sinc filter.
// Returned bytes are independent from input and internal buffers.
func (c *PCM16StreamConverter) Process(data []byte) ([]byte, error) {
	if c == nil || c.finished {
		return nil, fmt.Errorf("%w: converter is finished", ErrInvalidData)
	}
	if len(data) == 0 {
		return nil, nil
	}
	raw := make([]byte, 0, len(c.pending)+len(data))
	raw = append(raw, c.pending...)
	raw = append(raw, data...)
	frameSize := c.input.Channels * 2
	complete := len(raw) / frameSize * frameSize
	c.pending = append(c.pending[:0], raw[complete:]...)
	frames := raw[:complete]
	if c.input.SampleRate == c.output.SampleRate {
		return c.mapFrames(frames), nil
	}
	c.appendFrames(frames)
	return c.emit(false), nil
}

// Flush ends the input. It detects truncated PCM16 frames and uses edge
// extension for the last filter window. Flush can be called only once.
func (c *PCM16StreamConverter) Flush() ([]byte, error) {
	if c == nil || c.finished {
		return nil, fmt.Errorf("%w: converter is finished", ErrInvalidData)
	}
	c.finished = true
	if len(c.pending) > 0 {
		return nil, fmt.Errorf("%w: incomplete final PCM frame", ErrInvalidData)
	}
	if c.input.SampleRate == c.output.SampleRate {
		return nil, nil
	}
	return c.emit(true), nil
}

func (c *PCM16StreamConverter) appendFrames(frames []byte) {
	n := len(frames) / (c.input.Channels * 2)
	for i := 0; i < n; i++ {
		offset := i * c.input.Channels * 2
		first := float64(int16(binary.LittleEndian.Uint16(frames[offset : offset+2])))
		if c.input.Channels == 2 {
			second := float64(int16(binary.LittleEndian.Uint16(frames[offset+2 : offset+4])))
			if c.output.Channels == 2 {
				c.samples = append(c.samples, first, second)
			} else {
				c.samples = append(c.samples, (first+second)/2)
			}
		} else if c.output.Channels == 2 {
			c.samples = append(c.samples, first, first)
		} else {
			c.samples = append(c.samples, first)
		}
	}
	c.received += int64(n)
}

func (c *PCM16StreamConverter) mapFrames(frames []byte) []byte {
	if c.input.Channels == c.output.Channels {
		return append([]byte(nil), frames...)
	}
	n := len(frames) / (c.input.Channels * 2)
	out := make([]byte, n*c.output.Channels*2)
	for i := 0; i < n; i++ {
		first := int16(binary.LittleEndian.Uint16(frames[i*c.input.Channels*2:]))
		if c.input.Channels == 2 {
			second := int16(binary.LittleEndian.Uint16(frames[i*4+2:]))
			mixed := (int32(first) + int32(second)) / 2
			binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(mixed)))
		} else {
			binary.LittleEndian.PutUint16(out[i*4:], uint16(first))
			binary.LittleEndian.PutUint16(out[i*4+2:], uint16(first))
		}
	}
	return out
}

func (c *PCM16StreamConverter) emit(final bool) []byte {
	if c.received == 0 {
		return nil
	}
	ratio := float64(c.input.SampleRate) / float64(c.output.SampleRate)
	cutoff := math.Min(1, 1/ratio) * 0.94
	out := make([]byte, 0)
	for {
		pos := float64(c.next) * ratio
		if final {
			// Preserve the source duration, including upsampling near EOF.
			if pos >= float64(c.received) {
				break
			}
		} else if pos+sincRadius > float64(c.received-1) {
			break
		}
		center := int64(math.Floor(pos))
		var sums [2]float64
		totalWeight := 0.0
		for i := center - sincRadius + 1; i <= center+sincRadius; i++ {
			distance := pos - float64(i)
			if math.Abs(distance) >= sincRadius {
				continue
			}
			weight := cutoff * sinc(cutoff*distance) * (0.5 + 0.5*math.Cos(math.Pi*distance/sincRadius))
			totalWeight += weight
			index := i
			if index < 0 {
				index = 0
			}
			if index >= c.received {
				index = c.received - 1
			}
			if index < c.base {
				index = c.base
			}
			baseIdx := int(index-c.base) * c.output.Channels
			for ch := 0; ch < c.output.Channels; ch++ {
				sums[ch] += c.samples[baseIdx+ch] * weight
			}
		}
		for ch := 0; ch < c.output.Channels; ch++ {
			v := sums[ch]
			if totalWeight != 0 {
				v /= totalWeight
			}
			value := int64(math.Round(v))
			if value > math.MaxInt16 {
				value = math.MaxInt16
			}
			if value < math.MinInt16 {
				value = math.MinInt16
			}
			out = append(out, byte(uint16(value)), byte(uint16(value)>>8))
		}
		c.next++
	}
	if !final {
		c.trimHistory(ratio)
	} else {
		c.samples = nil
		c.base = c.received
	}
	return out
}
func sinc(x float64) float64 {
	if math.Abs(x) < 1e-12 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}
func (c *PCM16StreamConverter) trimHistory(ratio float64) {
	keep := int64(math.Floor(float64(c.next)*ratio)) - sincRadius - 2
	if keep <= c.base {
		return
	}
	if keep > c.received {
		keep = c.received
	}
	drop := int(keep-c.base) * c.output.Channels
	// Keep memory bounded even for long-lived voice streams.
	c.samples = append([]float64(nil), c.samples[drop:]...)
	c.base = keep
}

// ConvertPCM16 converts one complete PCM16LE or PCM16-in-WAV buffer to raw
// PCM16LE or WAV, resampling and changing channels if necessary. It does not
// decode MP3, AAC, Opus, Speex or AMR; do not mistake format metadata for a
// transcoder. Use provider passthrough or supply an external codec backend.
func ConvertPCM16(input Buffer, target Format) (Buffer, error) {
	if err := target.Validate(); err != nil {
		return Buffer{}, err
	}
	if target.Encoding != EncodingPCM16LE && target.Encoding != EncodingWAV {
		return Buffer{}, fmt.Errorf("%w: target must be PCM16LE or WAV", ErrUnsupportedConversion)
	}
	var source Buffer
	switch input.Format.Encoding {
	case EncodingPCM16LE:
		if err := ValidateBuffer(input); err != nil {
			return Buffer{}, err
		}
		source = input
	case EncodingWAV:
		raw, err := DecodeWAVPCM16(input.Data)
		if err != nil {
			return Buffer{}, err
		}
		if raw.Format.SampleRate != input.Format.SampleRate || raw.Format.Channels != input.Format.Channels {
			return Buffer{}, fmt.Errorf("%w: supplied WAV metadata does not match header", ErrInvalidData)
		}
		source = raw
	default:
		return Buffer{}, fmt.Errorf("%w: no decoder for %q", ErrUnsupportedConversion, input.Format.Encoding)
	}
	outputFormat := Format{Encoding: EncodingPCM16LE, SampleRate: target.SampleRate, Channels: target.Channels}
	converter, err := NewPCM16StreamConverter(source.Format, outputFormat)
	if err != nil {
		return Buffer{}, err
	}
	first, err := converter.Process(source.Data)
	if err != nil {
		return Buffer{}, err
	}
	tail, err := converter.Flush()
	if err != nil {
		return Buffer{}, err
	}
	result := Buffer{Data: append(first, tail...), Format: outputFormat}
	if target.Encoding == EncodingWAV {
		return EncodeWAVPCM16(result)
	}
	return result, nil
}
