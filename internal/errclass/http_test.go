package errclass

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/NSObjects/go-speech/speecherr"
)

func TestHTTPStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		code speecherr.Code
		retry bool
	}{
		{200, speecherr.CodeProtocol, false},
		{302, speecherr.CodeProtocol, false},
		{400, speecherr.CodeInvalidArgument, false},
		{401, speecherr.CodeUnauthorized, false},
		{402, speecherr.CodeRateLimited, false},
		{403, speecherr.CodeUnauthorized, false},
		{404, speecherr.CodeInvalidArgument, false},
		{408, speecherr.CodeTimeout, true},
		{422, speecherr.CodeInvalidArgument, false},
		{429, speecherr.CodeRateLimited, true},
		{500, speecherr.CodeUnavailable, true},
		{501, speecherr.CodeUnsupported, false},
		{503, speecherr.CodeUnavailable, true},
		{504, speecherr.CodeTimeout, true},
	} {
		got, retry := Dial(errors.New("bad handshake"), &http.Response{StatusCode: tc.status})
		if got != tc.code || retry != tc.retry {
			t.Errorf("HTTP %d: %s retry=%t; want %s retry=%t", tc.status, got, retry, tc.code, tc.retry)
		}
	}
}

func TestDialWithoutHTTPResponse(t *testing.T) {
	for _, tc := range []struct {
		err error
		code speecherr.Code
		retry bool
	}{
		{context.Canceled, speecherr.CodeUnavailable, false},
		{context.DeadlineExceeded, speecherr.CodeTimeout, true},
		{errors.New("connection refused"), speecherr.CodeUnavailable, true},
	} {
		got, retry := Dial(tc.err, nil)
		if got != tc.code || retry != tc.retry {
			t.Errorf("%v: %s retry=%t", tc.err, got, retry)
		}
	}
}
