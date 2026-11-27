package tencent

import (
	"context"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

var _ asr.Client = (*Client)(nil)

// Recognize returns confirmed text for a complete short audio buffer.
// The same provider session and audio format restrictions as OpenStream apply.
func (c *Client) Recognize(ctx context.Context, input audio.Buffer, cfg asr.Config) (asr.Result, error) {
	if input.Format.Encoding != EncodingOpusFrames && input.Format.Encoding != audio.EncodingMP3 {
		return asr.RecognizeStream(ctx, c, input, cfg)
	}
	if err := ctx.Err(); err != nil {
		return asr.Result{}, err
	}
	if cfg.Format != (audio.Format{}) && cfg.Format != input.Format {
		return asr.Result{}, argumentError("config format differs from input format")
	}
	cfg.Format = input.Format
	if err := c.ValidateConfig(cfg); err != nil {
		return asr.Result{}, err
	}
	if input.Format.Encoding == audio.EncodingMP3 {
		frames, err := mp3Frames(input.Data)
		if err != nil {
			return asr.Result{}, err
		}
		return asr.RecognizePacketStream(ctx, c, cfg, frames, 36*time.Millisecond)
	}
	frames, err := nativeOpusFrames(input.Data)
	if err != nil {
		return asr.Result{}, err
	}
	return asr.RecognizePacketStream(ctx, c, cfg, frames, nativeOpusDuration)
}
