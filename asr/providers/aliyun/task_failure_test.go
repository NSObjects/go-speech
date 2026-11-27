package aliyun

import (
	"errors"
	"testing"

	"github.com/NSObjects/go-speech/speecherr"
)

// DashScope task-failed carries a structured error_code. Remote messages
// are not stable and must not override a known error-code classification.
func TestTaskFailureDashScopeCodes(t *testing.T) {
	for _, tc := range []struct {
		name, remote, message string
		want speecherr.Code
		retryable bool
	}{
		{"permanent service quota", "ServiceQuotaExhausted", "retry later", speecherr.CodeRateLimited, false},
		{"request timeout", "RequestTimeout", "timed out", speecherr.CodeTimeout, true},
		{"server error", "InternalServerError", "temporary", speecherr.CodeUnavailable, true},
		{"invalid API key", "InvalidApiKey", "incorrect credential", speecherr.CodeUnauthorized, false},
		{"authentication failed legacy", "AuthenticationFailed", "test failure", speecherr.CodeUnauthorized, false},
		{"snake case API key", "invalid_api_key", "bad key", speecherr.CodeUnauthorized, false},
		{"access denied", "AccessDenied", "permission required", speecherr.CodeUnauthorized, false},
		{"invalid parameter", "InvalidParameter", "wrong sample_rate", speecherr.CodeInvalidArgument, false},
		{"nested invalid parameter", "InvalidParameter.Audio", "unsupported audio", speecherr.CodeInvalidArgument, false},
		{"missing model", "ModelNotFound", "model absent", speecherr.CodeInvalidArgument, false},
		{"request throttling", "Throttling.RateQuota", "rate exceeded", speecherr.CodeRateLimited, true},
		{"generic throttling legacy", "Throttling", "test failure", speecherr.CodeRateLimited, true},
		{"burst throttling", "Throttling.BurstRate", "slow down", speecherr.CodeRateLimited, true},
		{"allocation exhausted", "Throttling.AllocationQuota", "quota exceeded", speecherr.CodeRateLimited, false},
		{"billing issue", "Arrearage", "payment needed", speecherr.CodeRateLimited, false},
		{"temporary outage", "ServiceUnavailable", "service busy", speecherr.CodeUnavailable, true},
		{"known code beats message", "InvalidParameter", "authentication timeout quota", speecherr.CodeInvalidArgument, false},
		{"internal error needs diagnostics", "InternalError", "remote internal error", speecherr.CodeInternal, false},
		{"unknown code with timeout message", "SomethingNew", "upload timeout", speecherr.CodeTimeout, false},
		{"unknown code preserved", "FutureCode", "unexpected", speecherr.CodeInternal, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := taskFailure(responseHeader{ErrorCode: tc.remote, ErrorMessage: tc.message})
			if !speecherr.IsCode(got, tc.want) {
				t.Fatalf("code %q classified %v; want %s", tc.remote, got, tc.want)
			}
			var detail *speecherr.Error
			if !errors.As(got, &detail) {
				t.Fatalf("not a structured error: %v", got)
			}
			if detail.RemoteCode != tc.remote || detail.Retryable != tc.retryable {
				t.Fatalf("code %q: %+v; expected Retryable=%t", tc.remote, detail, tc.retryable)
			}
		})
	}
}
