// Package errclass centralizes transport and shared provider error mappings.
// It never performs retries or network operations.
package errclass

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/NSObjects/go-speech/speecherr"
)

// HTTPStatus classifies an unsuccessful HTTP request or WebSocket upgrade.
// A retry hint is advisory; it cannot establish task replay safety.
func HTTPStatus(status int) (speecherr.Code, bool) {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return speecherr.CodeUnauthorized, false
	case status == http.StatusPaymentRequired:
		return speecherr.CodeRateLimited, false
	case status == http.StatusTooManyRequests:
		return speecherr.CodeRateLimited, true
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return speecherr.CodeTimeout, true
	case status == http.StatusNotImplemented:
		return speecherr.CodeUnsupported, false
	case status >= 400 && status < 500:
		return speecherr.CodeInvalidArgument, false
	case status >= 500 && status < 600:
		return speecherr.CodeUnavailable, true
	default:
		// A redirect or other unexpected upgrade response is not an outage.
		return speecherr.CodeProtocol, false
	}
}

// Dial classifies the HTTP response if available, or the transport cause
// when no response arrived. Callers retain their own context checks.
func Dial(err error, response *http.Response) (speecherr.Code, bool) {
	if response != nil {
		return HTTPStatus(response.StatusCode)
	}
	if errors.Is(err, context.Canceled) {
		return speecherr.CodeUnavailable, false
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout()) {
		return speecherr.CodeTimeout, true
	}
	return speecherr.CodeUnavailable, true
}
