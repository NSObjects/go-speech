package volcengine

import (
	"errors"
	"testing"

	"github.com/NSObjects/go-speech/speecherr"
)

func TestHTTPFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		code speecherr.Code
	}{
		{404, speecherr.CodeInvalidArgument},
		{402, speecherr.CodeRateLimited},
		{408, speecherr.CodeTimeout},
		{429, speecherr.CodeRateLimited},
		{503, speecherr.CodeUnavailable},
	} {
		var detail *speecherr.Error
		err := httpFailure(tc.status)
		if !errors.As(err, &detail) || detail.Code != tc.code || detail.Retryable {
			t.Errorf("HTTP %d: %v", tc.status, err)
		}
	}
}
