package speecherr

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestErrorWrapping(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("recognition failed: %w", &Error{
		Code: CodeTimeout, Provider: "vendor", Operation: "recognize",
		Retryable: true, Err: context.DeadlineExceeded,
	})
	if !IsCode(err, CodeTimeout) || IsCode(err, CodeUnauthorized) {
		t.Fatalf("unexpected error code: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("underlying cause lost: %v", err)
	}
	if IsCode(nil, CodeTimeout) {
		t.Fatal("nil must not match")
	}
}
