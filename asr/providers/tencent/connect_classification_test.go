package tencent

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/NSObjects/go-speech/speecherr"
)

func TestConnectionFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		code speecherr.Code
		retry bool
	}{
		{404, speecherr.CodeInvalidArgument, false},
		{402, speecherr.CodeRateLimited, false},
		{408, speecherr.CodeTimeout, true},
		{429, speecherr.CodeRateLimited, true},
		{503, speecherr.CodeUnavailable, true},
	} {
		err := connectError(errors.New("bad handshake"), &http.Response{StatusCode: tc.status})
		var detail *speecherr.Error
		if !errors.As(err, &detail) || detail.Code != tc.code || detail.Retryable != tc.retry {
			t.Errorf("HTTP %d: %+v", tc.status, detail)
		}
	}
	err := connectError(context.DeadlineExceeded, nil)
	var detail *speecherr.Error
	if !errors.As(err, &detail) || detail.Code != speecherr.CodeTimeout || !detail.Retryable {
		t.Fatalf("handshake timeout: %v", err)
	}
}

func TestConnectDiagnosticDoesNotLeakSignedURL(t *testing.T) {
	cause := &url.Error{Op: "dial", URL: "wss://example.com/?SecretId=private-id&Signature=private-signature", Err: errors.New("failure")}
	for _, response := range []*http.Response{nil, {StatusCode: 403}} {
		err := connectError(cause, response)
		if strings.Contains(err.Error(), "private-id") || strings.Contains(err.Error(), "private-signature") {
			t.Fatal("signed URL leaked in error")
		}
	}
}
