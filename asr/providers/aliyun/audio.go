package aliyun

import (
	"github.com/NSObjects/go-speech/audio"
)

// SupportedAudio reports the documented input formats accepted by the
// Aliyun ASR model. This does not imply automatic transcoding. In particular
// Opus/Speex mean Ogg encapsulation, WAV means PCM WAV, AMR means AMR-NB.
func SupportedAudio(model string) (audio.Capability, error) {
	if _, err := modelType(model); err != nil {
		return audio.Capability{}, err
	}
	capability := audio.Capability{
		Encodings: []audio.Encoding{
			audio.EncodingPCM16LE,
			audio.EncodingWAV,
			audio.EncodingMP3,
			audio.EncodingOpus,
			audio.EncodingSpeex,
			audio.EncodingAAC,
			audio.EncodingAMR,
		},
		Channels: []int{1},
	}
	switch model {
	case ModelQwenAudio31Message:
		capability.SampleRates = []int{16000}
	case ModelParaformerRealtime8K:
		capability.SampleRates = []int{8000}
	}
	return capability, nil
}
