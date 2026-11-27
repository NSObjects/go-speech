// Package tencent implements Tencent Cloud VRS voice enrollment APIs.
package tencent

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

	"github.com/NSObjects/go-speech/internal/tc3"
	"github.com/NSObjects/go-speech/internal/wireutil"
	"github.com/NSObjects/go-speech/speecherr"
)

const (
	DefaultEndpoint  = "https://vrs.tencentcloudapi.com"
	version          = "2020-08-24"
	contentType      = tc3.ContentType
	maxResponseBytes = 8 << 20
)

// Config selects Cloud API credentials. Token is optional for temporary
// credentials; AppID is not needed by this HTTP API. Timeout defaults to 120s.
type Config struct {
	SecretID  string
	SecretKey string
	Token     string
	Region    string
	Endpoint  string
	Timeout   time.Duration
}

type Client struct {
	cfg  Config
	base *url.URL
	http *http.Client
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.SecretID) == "" || strings.TrimSpace(cfg.SecretKey) == "" ||
		strings.ContainsAny(cfg.SecretID, " ,/\r\n\t") || strings.ContainsAny(cfg.Token+cfg.Region, "\r\n") {
		return nil, invalid("invalid credentials or region")
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
	u.Path = "/"
	return &Client{cfg: cfg, base: u, http: &http.Client{
		Timeout: cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func authorization(id, key, host, action string, now time.Time, body []byte) string {
	return tc3.Authorization(id, key, host, action, "vrs", now, body)
}

type Metadata struct {
	RequestID string `json:"RequestId"`
}

type RemoteError struct {
	HTTPStatus int
	Code       string
	Message    string
	RequestID  string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("HTTP %d (code=%s, request_id=%s): %s", e.HTTPStatus, e.Code, e.RequestID, e.Message)
}

type envelope struct {
	Response *struct {
		Metadata
		Data json.RawMessage `json:"Data"`
		Error *struct {
			Code string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
	} `json:"Response"`
}

func failure(op string, code speecherr.Code, err error) error {
	return &speecherr.Error{Provider: "tencent", Operation: op, Code: code, Err: err}
}

func invalid(msg string) error {
	return failure("voice_validate", speecherr.CodeInvalidArgument, errors.New(msg))
}

func protocol(op, msg string) error {
	return failure(op, speecherr.CodeProtocol, errors.New(msg))
}

func (c *Client) call(ctx context.Context, action string, input, output any) (Metadata, error) {
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	if c == nil || c.base == nil || c.http == nil {
		return Metadata{}, invalid("uninitialized client")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Metadata{}, failure(action, speecherr.CodeInvalidArgument, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String(), bytes.NewReader(body))
	if err != nil {
		return Metadata{}, failure(action, speecherr.CodeInvalidArgument, err)
	}
	req.GetBody = nil
	now := time.Now()
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", version)
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("Authorization", authorization(c.cfg.SecretID, c.cfg.SecretKey, req.URL.Host, action, now, body))
	if c.cfg.Region != "" {
		req.Header.Set("X-TC-Region", c.cfg.Region)
	}
	if c.cfg.Token != "" {
		req.Header.Set("X-TC-Token", c.cfg.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Metadata{}, transportFailure(ctx, action, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Metadata{}, transportFailure(ctx, action, err)
	}
	if len(raw) > maxResponseBytes {
		return Metadata{}, protocol(action, "voice response exceeds limit")
	}
	var env envelope
	decodeErr := json.Unmarshal(raw, &env)
	if resp.StatusCode != http.StatusOK || env.Response != nil && env.Response.Error != nil {
		cause := &RemoteError{HTTPStatus: resp.StatusCode}
		if env.Response != nil {
			cause.RequestID = env.Response.RequestID
			if e := env.Response.Error; e != nil {
				cause.Code, cause.Message = e.Code, e.Message
			}
		}
		code := speecherr.CodeUnavailable
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403 || strings.HasPrefix(cause.Code, "AuthFailure") || strings.HasPrefix(cause.Code, "UnauthorizedOperation"):
			code = speecherr.CodeUnauthorized
		case resp.StatusCode == 429 || strings.HasPrefix(cause.Code, "RequestLimitExceeded"):
			code = speecherr.CodeRateLimited
		case resp.StatusCode == 408 || resp.StatusCode == 504:
			code = speecherr.CodeTimeout
		case resp.StatusCode >= 400 && resp.StatusCode < 500 || strings.HasPrefix(cause.Code, "InvalidParameter") || strings.HasPrefix(cause.Code, "MissingParameter"):
			code = speecherr.CodeInvalidArgument
		case resp.StatusCode == 200:
			code = speecherr.CodeInternal
		}
		return Metadata{}, &speecherr.Error{Provider: "tencent", Operation: action, Code: code, RemoteCode: cause.Code, Err: cause}
	}
	if decodeErr != nil || env.Response == nil {
		return Metadata{}, protocol(action, "invalid response envelope")
	}
	data := bytes.TrimSpace(env.Response.Data)
	if len(data) == 0 || data[0] != '{' || json.Unmarshal(data, output) != nil {
		return Metadata{}, protocol(action, "invalid response data")
	}
	return env.Response.Metadata, nil
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
