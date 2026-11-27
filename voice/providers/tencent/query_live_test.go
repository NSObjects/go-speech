//go:build integration

package tencent

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
)

func TestTencentLiveVoiceQueries(t *testing.T) {
	c, err := New(Config{
		SecretID: os.Getenv("TENCENT_SECRET_ID"), SecretKey: os.Getenv("TENCENT_SECRET_KEY"),
		Region: os.Getenv("TENCENT_VOICE_REGION"),
	})
	if err != nil {
		t.Fatal("invalid Tencent VRS client configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// Fetch fresh provider-issued training text without submitting audio or
	// creating a training task. Fast text IDs expire and are not reused.
	t.Run("training_text", func(t *testing.T) {
		domain, language := 1, 1
		for _, tc := range []struct {
			name string
			req  TrainingTextRequest
		}{
			{"basic", TrainingTextRequest{TaskType: Basic}},
			{"fast_chat", TrainingTextRequest{TaskType: Fast, Domain: &domain, TextLanguage: &language}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r, err := c.GetTrainingText(ctx, tc.req)
				if err != nil {
					failVoiceQuery(t, "training text", err)
				}
				if r.RequestID == "" || len(r.Texts) == 0 {
					t.Fatal("training text response has no request ID or text entries")
				}
				seen := make(map[string]bool, len(r.Texts))
				for _, item := range r.Texts {
					if !validID(item.TextID) || !validText(item.Text) || seen[item.TextID] {
						t.Fatal("invalid or duplicate provider-issued training text entry")
					}
					seen[item.TextID] = true
				}
				t.Logf("training text response returned %d usable entries", len(r.Texts))
			})
		}
	})
	for _, kind := range []TaskType{Basic, Fast} {
		name := "basic"
		if kind == Fast {
			name = "fast"
			// GetVRSVoiceTypes is limited to one request per second.
			// Space distinct list queries; never retry a rejected request.
			timer := time.NewTimer(1100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				t.Fatal(ctx.Err())
			case <-timer.C:
			}
		}
		t.Run(name, func(t *testing.T) {
			list, err := c.List(ctx, kind)
			if err != nil {
				failVoiceQuery(t, "list", err)
			}
			if list.RequestID == "" {
				t.Fatal("missing request ID")
			}
			t.Logf("voice list returned %d resources", len(list.Voices))
			t.Run("query", func(t *testing.T) {
				for _, item := range list.Voices {
					if item.VoiceType == nil || *item.VoiceType <= 0 {
						continue
					}
					ref := VoiceRef{VoiceType: *item.VoiceType, TaskType: kind}
					if kind == Fast {
						if item.FastVoiceType == "" {
							continue
						}
						ref.FastVoiceType = item.FastVoiceType
					}
					detail, err := c.Query(ctx, ref)
					if err != nil {
						failVoiceQuery(t, "detail query", err)
					}
					if detail.RequestID == "" {
						t.Fatal("missing detail request ID")
					}
					t.Logf("voice detail succeeded: state=%s", detail.State())
					taskID := detail.TaskID
					if taskID == "" {
						taskID = item.TaskID
					}
					t.Run("task", func(t *testing.T) {
						if taskID == "" {
							t.Skip("NOT VALIDATED: no existing training task ID")
						}
						task, err := c.QueryTask(ctx, taskID)
						if err != nil {
							failVoiceQuery(t, "task query", err)
						}
						if task.RequestID == "" || task.Status == nil || task.TaskID != taskID {
							t.Fatal("invalid task detail response")
						}
						t.Logf("task query succeeded: state=%s", task.State())
					})
					return
				}
				t.Skip("NOT VALIDATED: no existing queryable voice")
			})
		})
	}
}

// Report only structured diagnostic fields, not remote messages, credentials,
// authorization headers, or the response body.
func failVoiceQuery(t *testing.T, stage string, err error) {
	t.Helper()
	var classified *speecherr.Error
	var remote *RemoteError
	if errors.As(err, &classified) {
		if errors.As(err, &remote) {
			t.Fatalf("%s failed: code=%s operation=%q http_status=%d remote_code=%q request_id=%q",
				stage, classified.Code, classified.Operation, remote.HTTPStatus, remote.Code, remote.RequestID)
		}
		t.Fatalf("%s failed: code=%s operation=%q cause_type=%T", stage, classified.Code, classified.Operation, classified.Err)
	}
	t.Fatalf("%s failed: %T canceled=%t deadline=%t", stage, err, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded))
}
