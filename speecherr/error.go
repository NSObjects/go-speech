// Package speecherr defines provider-independent errors for ASR and TTS adapters.
package speecherr

import (
	"errors"
	"fmt"
)

// Code classifies provider errors. These codes are intentionally generic.
type Code string

const (
	CodeInvalidArgument Code = "invalid_argument"
	CodeUnauthorized    Code = "unauthorized"
	CodeRateLimited     Code = "rate_limited"
	CodeUnavailable     Code = "unavailable"
	CodeTimeout         Code = "timeout"
	CodeUnsupported     Code = "unsupported"
	CodeInternal        Code = "internal"
)

// Error wraps a provider failure while preserving its original cause.
// Retryable is advisory; callers must still consider idempotency and context.
type Error struct {
	Code      Code
	Provider  string
	Operation string
	Retryable bool
	Err       error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := fmt.Sprintf("speech: %s/%s: %s", e.Provider, e.Operation, e.Code)
	if e.Err != nil {
		return msg + ": " + e.Err.Error()
	}
	return msg
}

// Unwrap preserves errors.Is and errors.As for the underlying error.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsCode reports whether err or one of its wrapped causes is a speech Error
// with the given code.
func IsCode(err error, code Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}
