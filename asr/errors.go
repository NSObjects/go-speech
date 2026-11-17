package asr

import "errors"

var (
	// ErrStreamClosed means a stream has been explicitly closed or aborted.
	// It is not a transient provider/network failure and should not be retried
	// as another Send on the same stream.
	ErrStreamClosed = errors.New("asr: stream closed")

	// ErrInputClosed means CloseSend has already completed successfully.
	// Recv may still be used to drain final recognition events.
	ErrInputClosed = errors.New("asr: audio input closed")
)
