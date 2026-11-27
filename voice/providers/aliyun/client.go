// Package aliyun implements the DashScope voice-enrollment HTTP API.
package aliyun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NSObjects/go-speech/internal/wireutil"
	"github.com/NSObjects/go-speech/speecherr"
)

const DefaultEndpoint = "https://dashscope.aliyuncs.com"
const customizationPath = "/api/v1/services/audio/tts/customization"
const maxResponseBytes = 16 << 20

// Config selects an API key and regional or workspace HTTPS origin.
// Timeout defaults to 120 seconds and covers the entire operation.
type Config struct {
	APIKey   string
	Endpoint string
	Timeout  time.Duration
}

type Client struct {
	key  string
	base *url.URL
	http *http.Client
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" || strings.ContainsAny(cfg.APIKey, "\r\n") {
		return nil, invalid("APIKey is required")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") ||
		(u.Scheme != "https" && !(u.Scheme == "http" && wireutil.IsLoopback(u.Hostname()))) {
		return nil, invalid("Endpoint must be an HTTPS origin")
	}
	if cfg.Timeout < 0 {
		return nil, invalid("negative Timeout")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 120 * time.Second
	}
	return &Client{key: cfg.APIKey, base: u, http: &http.Client{
		Timeout: cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func failure(op string, code speecherr.Code, err error) error {
	return &speecherr.Error{Provider: "aliyun", Operation: op, Code: code, Err: err}
}

func invalid(msg string) error {
	return failure("voice_validate", speecherr.CodeInvalidArgument, errors.New(msg))
}

func unsupported(msg string) error {
	return failure("voice_validate", speecherr.CodeUnsupported, errors.New(msg))
}

func protocol(op, msg string) error {
	return failure(op, speecherr.CodeProtocol, errors.New(msg))
}

// RemoteError contains server diagnostics. A failed mutation can be
// ambiguous; the SDK never automatically retries it.
type RemoteError struct {
	HTTPStatus int
	Code       string
	Message    string
	RequestID  string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("HTTP %d (code=%s, request_id=%s): %s", e.HTTPStatus, e.Code, e.RequestID, e.Message)
}

type request struct {
	Model      string         `json:"model"`
	Input      map[string]any `json:"input"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type response struct {
	Metadata
	Output  json.RawMessage `json:"output"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
}

func (c *Client) call(ctx context.Context, input, parameters map[string]any, out any) (Metadata, error) {
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	if c == nil || c.base == nil || c.http == nil {
		return Metadata{}, invalid("uninitialized client")
	}
	op, _ := input["action"].(string)
	body, err := json.Marshal(request{Model: "voice-enrollment", Input: input, Parameters: parameters})
	if err != nil {
		return Metadata{}, failure(op, speecherr.CodeInvalidArgument, err)
	}
	u := *c.base
	u.Path = customizationPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return Metadata{}, failure(op, speecherr.CodeInvalidArgument, err)
	}
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Metadata{}, transportFailure(ctx, op, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Metadata{}, transportFailure(ctx, op, err)
	}
	if len(raw) > maxResponseBytes {
		return Metadata{}, protocol(op, "voice response exceeds limit")
	}
	var v response
	decodeErr := json.Unmarshal(raw, &v)
	if resp.StatusCode != http.StatusOK || v.Code != "" {
		cause := &RemoteError{HTTPStatus: resp.StatusCode, Code: v.Code, Message: v.Message, RequestID: v.RequestID}
		code := speecherr.CodeUnavailable
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403 || v.Code == "InvalidApiKey" || v.Code == "AccessDenied":
			code = speecherr.CodeUnauthorized
		case resp.StatusCode == 429 || strings.HasPrefix(v.Code, "Throttling"):
			code = speecherr.CodeRateLimited
		case resp.StatusCode == 408 || resp.StatusCode == 504:
			code = speecherr.CodeTimeout
		case resp.StatusCode >= 400 && resp.StatusCode < 500 || strings.HasPrefix(v.Code, "InvalidParameter"):
			code = speecherr.CodeInvalidArgument
		case resp.StatusCode == 200:
			code = speecherr.CodeInternal
		}
		return Metadata{}, &speecherr.Error{Provider: "aliyun", Operation: op, Code: code, RemoteCode: v.Code, Err: cause}
	}
	output := bytes.TrimSpace(v.Output)
	if decodeErr != nil || len(output) == 0 || output[0] != '{' || json.Unmarshal(output, out) != nil {
		return Metadata{}, protocol(op, "invalid voice response")
	}
	return v.Metadata, nil
}

func transportFailure(ctx context.Context, op string, err error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return failure(op, speecherr.CodeTimeout, err)
	}
	return failure(op, speecherr.CodeUnavailable, err)
}
