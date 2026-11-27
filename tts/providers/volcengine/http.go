package volcengine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
)

type synthesisRequest struct {
	Text              string         `json:"text,omitempty"`
	SSML              string         `json:"ssml,omitempty"`
	ModelID           string         `json:"model_id"`
	LanguageCode      string         `json:"language_code"`
	OutputFormat      outputFormat   `json:"output_format"`
	VoiceSettings     *voiceSettings `json:"voice_settings,omitempty"`
	SessionID         string         `json:"session_id,omitempty"`
	SilenceDurationMS *float64       `json:"silence_duration,omitempty"`
	advancedRequest
}

// SynthesizeStream sends complete text and decodes HTTP JSONL incrementally.
func (c *Client) SynthesizeStream(ctx context.Context, req tts.Request) (tts.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	format, data, err := c.prepareRequest(req)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	request, err := http.NewRequestWithContext(runCtx, http.MethodPost, c.endpoint(req.Voice, "streaming", false), bytes.NewReader(data))
	if err != nil {
		cancel()
		return nil, err
	}
	request.Header.Set("X-Api-Key", c.cfg.APIKey)
	request.Header.Set("Content-Type", "application/json")
	timer := time.AfterFunc(c.cfg.StartTimeout, cancel)
	response, err := c.http.Do(request)
	started := timer.Stop()
	if err != nil || !started {
		cancel()
		if response != nil {
			_ = response.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !started {
			return nil, context.DeadlineExceeded
		}
		return nil, failure(speecherr.CodeUnavailable, "request", err)
	}
	if response.StatusCode != http.StatusOK {
		defer cancel()
		defer func() { _ = response.Body.Close() }()
		// Bound diagnostic reads separately from the successful stream.
		bodyTimer := time.AfterFunc(c.cfg.StartTimeout, cancel)
		defer bodyTimer.Stop()
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if readErr == nil {
			if e, decodeErr := decodeEvent(raw); decodeErr == nil && e.Event == "error" {
				return nil, e.failure()
			}
		}
		return nil, httpFailure(response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	next := func() (event, error) {
		for scanner.Scan() {
			if len(bytes.TrimSpace(scanner.Bytes())) > 0 {
				return decodeEvent(scanner.Bytes())
			}
		}
		if err := scanner.Err(); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return event{}, err
			}
			return event{}, failure(speecherr.CodeProtocol, "receive", err)
		}
		return event{}, io.EOF
	}
	return newStream(ctx, format, next, func() {
		cancel()
		_ = response.Body.Close()
	}), nil
}
