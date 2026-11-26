package tts

import (
	"context"

	"github.com/NSObjects/go-speech/audio"
)

// DuplexRequest configures an incremental-text synthesis session.
// Voice identifiers and output formats are specific to each provider.
type DuplexRequest struct {
	Voice      string
	Format     audio.Format
	TextFormat TextFormat
}

// DuplexSynthesizer opens a session that accepts incremental text while
// synthesized audio is read concurrently.
type DuplexSynthesizer interface {
	OpenDuplex(ctx context.Context, req DuplexRequest) (DuplexStream, error)
}

// DuplexStream supports one text sender and one audio receiver concurrently.
// SendText is called with successive UTF-8 fragments; it is not safe to call
// SendText and CloseSend concurrently. CloseSend finalizes text input while
// Recv continues until io.EOF. Always call Close to free resources.
type DuplexStream interface {
	Stream
	SendText(ctx context.Context, text string) error
	CloseSend(ctx context.Context) error
}
