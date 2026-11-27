package aliyun

import (
	"context"
	"encoding/json"
	"math"

	"github.com/NSObjects/go-speech/asr"
)

// ContextUpdater is implemented by Aliyun streams. Only Qwen-Audio-ASR-Message
// supports the native continue-task context update. Serialize UpdateContext
// with Send and CloseSend. It affects subsequent audio, not prior transcripts.
// A nil error confirms that the command was sent, not that recognition improved.
type ContextUpdater interface {
	UpdateContext(context.Context, []ContextMessage) error
}

var (
	_ ContextUpdater              = (*Stream)(nil)
	_ asr.SessionMetadataReceiver = (*Stream)(nil)
)

func (s *Stream) UpdateContext(ctx context.Context, messages []ContextMessage) error {
	if s == nil {
		return ErrClosed
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed() {
		return s.closeError(ctx)
	}
	if s.sendClosed {
		return ErrSendClosed
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	f, err := modelType(s.model)
	if err != nil || !f.supportsDynamicContext() {
		return unsupported("dynamic context requires Qwen-Audio-ASR-Message")
	}
	if len(messages) == 0 {
		return badArgument("context update requires at least one message")
	}
	input, err := (Config{Model: s.model}).contextInput(messages)
	if err != nil {
		return err
	}
	cmd := taskMessage{
		Header: commandHeader{Action: "continue-task", TaskID: s.taskID, Streaming: "duplex"},
		Payload: taskPayload{Input: input},
	}
	if err := s.writeJSON(ctx, cmd); err != nil {
		return s.fail(s.transportError(ctx, "continue-task", err))
	}
	return nil
}

// Usage is a provider-reported cumulative snapshot, never a sum of events.
// Missing fields are nil, including on duration-only models. Final is true
// only when this snapshot came from task-finished.
type Usage struct {
	DurationSeconds *float64 `json:"duration,omitempty"`
	InputTokens     *int64   `json:"input_tokens,omitempty"`
	OutputTokens    *int64   `json:"output_tokens,omitempty"`
	TotalTokens     *int64   `json:"total_tokens,omitempty"`
	Final           bool     `json:"-"`
}

// SessionDetails contains the latest available usage for one recognition task.
type SessionDetails struct {
	Usage Usage
}

// SessionMetadata returns an independent usage snapshot without acquiring
// readMu or waiting on a network read. It returns nil if no usable usage has
// arrived. A task-finished event without usage does not promote an earlier
// snapshot to final.
func (s *Stream) SessionMetadata() any {
	if s == nil {
		return nil
	}
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if s.usage == nil {
		return nil
	}
	return SessionDetails{Usage: cloneUsage(*s.usage)}
}

func cloneUsage(u Usage) Usage {
	cloneInt := func(v *int64) *int64 {
		if v == nil {
			return nil
		}
		x := *v
		return &x
	}
	if u.DurationSeconds != nil {
		x := *u.DurationSeconds
		u.DurationSeconds = &x
	}
	u.InputTokens = cloneInt(u.InputTokens)
	u.OutputTokens = cloneInt(u.OutputTokens)
	u.TotalTokens = cloneInt(u.TotalTokens)
	return u
}

// Parse optional counters independently: an unrecognized or malformed usage
// field must not discard a valid transcript. Keep zero distinct from absent,
// and never manufacture a total from incomplete fields.
func (s *Stream) recordUsage(raw json.RawMessage, final bool) {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return
	}
	var u Usage
	if v := fields["duration"]; len(v) != 0 {
		var seconds *float64
		if json.Unmarshal(v, &seconds) == nil && seconds != nil && *seconds >= 0 && !math.IsInf(*seconds, 0) && !math.IsNaN(*seconds) {
			u.DurationSeconds = seconds
		}
	}
	counter := func(key string) *int64 {
		var value *int64
		if json.Unmarshal(fields[key], &value) != nil || value == nil || *value < 0 {
			return nil
		}
		return value
	}
	u.InputTokens = counter("input_tokens")
	u.OutputTokens = counter("output_tokens")
	u.TotalTokens = counter("total_tokens")
	if u.DurationSeconds == nil && u.InputTokens == nil && u.OutputTokens == nil && u.TotalTokens == nil {
		return
	}
	u.Final = final
	s.usageMu.Lock()
	s.usage = &u
	s.usageMu.Unlock()
}
