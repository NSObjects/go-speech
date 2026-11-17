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

func TestProviderCodeIsPreserved(t *testing.T) {
	err := &Error{
		Code: CodeRateLimited, Provider: "example", Operation: "stream",
		RemoteCode: "Throttling", Retryable: true,
	}
	if !IsCode(fmt.Errorf("outer: %w", err), CodeRateLimited) {
		t.Fatal("lost generic error classification")
	}
	if err.RemoteCode != "Throttling" {
		t.Fatalf("lost original provider error code: %s", err.RemoteCode)
	}
	if IsCode(err, CodeProtocol) {
		t.Fatal("protocol code must differ from rate limiting")
	}
}
