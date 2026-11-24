package volcengine

import (
	"context"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

var _ asr.Client = (*Client)(nil)

// Recognize returns confirmed text for a complete short audio buffer.
// The same provider session and audio format restrictions as OpenStream apply.
func (c *Client) Recognize(ctx context.Context, input audio.Buffer, cfg asr.Config) (asr.Result, error) {
	return asr.RecognizeStream(ctx, c, input, cfg)
}
