package aliyun

import (
	"errors"
	"testing"

	"github.com/NSObjects/go-speech/speecherr"
)

func TestCosyVoiceStructuredTaskFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
		message string
		want speecherr.Code
		retry bool
	}{
		{"throttle", "Throttling", "request too frequent", speecherr.CodeRateLimited, true},
		{"quota", "QuotaExceeded", "please retry later", speecherr.CodeRateLimited, false},
		{"allocation", "AllocationQuotaExceeded", "try again", speecherr.CodeRateLimited, false},
		{"auth", "InvalidApiKey", "rate limit has been hit", speecherr.CodeUnauthorized, false},
		{"permission", "AccessDenied", "permission denied", speecherr.CodeUnauthorized, false},
		{"parameter", "InvalidParameter", "invalid input", speecherr.CodeInvalidArgument, false},
		{"model", "ModelNotFound", "no such model", speecherr.CodeInvalidArgument, false},
		{"timeout", "RequestTimeout", "timed out", speecherr.CodeTimeout, true},
		{"server", "InternalServerError", "temporarily unavailable", speecherr.CodeUnavailable, true},
		{"unknown", "FutureVendorCode", "quota exceeded", speecherr.CodeInternal, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyTaskFailure(serverHeader{ErrorCode: tc.code, ErrorMessage: tc.message})
			if !speecherr.IsCode(err, tc.want) {
				t.Fatalf("classification = %v, want %s", err, tc.want)
			}
			var providerErr *speecherr.Error
			if !errors.As(err, &providerErr) || providerErr.RemoteCode != tc.code || providerErr.Retryable != tc.retry {
				t.Fatalf("remote details = %+v, want code %q retryable %t", providerErr, tc.code, tc.retry)
			}
		})
	}
}
