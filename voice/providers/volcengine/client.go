package volcengine

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
	"strconv"
	"strings"
	"time"

	"github.com/NSObjects/go-speech/internal/wireutil"
	"github.com/NSObjects/go-speech/speecherr"
)

const DefaultEndpoint = "https://voice.cn-beijing.volces.com"
const maxResponseBytes = 1 << 20

// Config configures voice management independently of TTS synthesis.
// Timeout applies to a complete HTTP operation; zero defaults to 120 seconds.
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
		return nil, invalid("Endpoint must be an HTTPS origin (HTTP loopback for tests)")
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

// RemoteError preserves response status and server diagnostics without
// storing the reference recording, API key, or raw request body.
type RemoteError struct {
	HTTPStatus int
	Code       *int
	Message    string
	LogID      string
}

func (e *RemoteError) Error() string {
	code := ""
	if e.Code != nil {
		code = fmt.Sprintf(", remote code %d", *e.Code)
	}
	return fmt.Sprintf("HTTP %d%s (log_id=%s): %s", e.HTTPStatus, code, e.LogID, e.Message)
}

type response struct {
	Result
	Code    *int   `json:"code"`
	Message string `json:"message"`
}

func failure(op string, code speecherr.Code, err error) error {
	// A failed response to a mutation can be ambiguous. Do not mark an
	// enrollment or design request retryable simply because HTTP failed.
	return &speecherr.Error{Code: code, Provider: "volcengine", Operation: op, Err: err}
}

func (c *Client) call(ctx context.Context, op string, body any) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if c == nil || c.base == nil || c.http == nil {
		return Result{}, invalid("uninitialized client")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Result{}, failure(op, speecherr.CodeInvalidArgument, err)
	}
	id, err := wireutil.UUIDv4()
	if err != nil {
		return Result{}, failure(op, speecherr.CodeInternal, err)
	}
	u := *c.base
	u.Path = "/api/v3/tts/" + op
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return Result{}, failure(op, speecherr.CodeInvalidArgument, err)
	}
	// Do not permit net/http to replay a body after a transport failure.
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", c.key)
	req.Header.Set("X-Api-Request-Id", id)
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, transportFailure(ctx, op, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Result{}, transportFailure(ctx, op, err)
	}
	if len(raw) > maxResponseBytes {
		return Result{}, failure(op, speecherr.CodeProtocol, errors.New("voice response exceeds limit"))
	}
	var out response
	decodeErr := json.Unmarshal(raw, &out)
	logID := resp.Header.Get("X-Tt-Logid")
	if resp.StatusCode != http.StatusOK || (out.Code != nil && *out.Code != 0) {
		cause := &RemoteError{HTTPStatus: resp.StatusCode, Code: out.Code, Message: out.Message, LogID: logID}
		code := speecherr.CodeUnavailable
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			code = speecherr.CodeUnauthorized
		case resp.StatusCode == 429:
			code = speecherr.CodeRateLimited
		case resp.StatusCode == 408 || resp.StatusCode == 504:
			code = speecherr.CodeTimeout
		case resp.StatusCode >= 400 && resp.StatusCode < 500:
			code = speecherr.CodeInvalidArgument
		case resp.StatusCode == 200:
			code = speecherr.CodeInternal
		}
		e := failure(op, code, cause).(*speecherr.Error)
		if out.Code != nil {
			e.RemoteCode = strconv.Itoa(*out.Code)
		}
		return Result{}, e
	}
	if decodeErr != nil || out.Status == nil || (out.SpeakerID == "" && *out.Status != 0) {
		return Result{}, failure(op, speecherr.CodeProtocol, errors.New("invalid voice response"))
	}
	out.LogID = logID
	return out.Result, nil
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
