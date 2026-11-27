package connpool

import (
	"context"
	"errors"

	"github.com/NSObjects/go-speech/speecherr"
)

// ClassifyError converts pool-specific failures at the provider boundary.
// Preserve caller cancellation, existing provider errors and underlying
// causes. A pool-local deadline is a timeout, not a remote rate limit.
func ClassifyError(ctx context.Context, provider, operation string, err error) error {
	if err == nil {
		return nil
	}
	var classified *speecherr.Error
	if errors.As(err, &classified) {
		return err
	}
	var code speecherr.Code
	switch {
	case errors.Is(err, ErrClosed):
		code = speecherr.CodeUnavailable
	case errors.Is(err, ErrDisabled):
		code = speecherr.CodeUnsupported
	case errors.Is(err, ErrInvalidWarmupCount):
		code = speecherr.CodeInvalidArgument
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		code = speecherr.CodeTimeout
	default:
		return err
	}
	return &speecherr.Error{Code: code, Provider: provider, Operation: operation, Err: err}
}
