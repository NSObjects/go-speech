//go:build integration

package tencent

import (
	"context"
	"os"
	"testing"
	"time"
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
	for _, kind := range []TaskType{Basic, Fast} {
		name := "basic"
		if kind == Fast {
			name = "fast"
		}
		t.Run(name, func(t *testing.T) {
			list, err := c.List(ctx, kind)
			if err != nil {
				t.Fatalf("list failed: %T (inspect provider console)", err)
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
						t.Fatalf("detail query failed: %T (inspect provider console)", err)
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
							t.Fatalf("task query failed: %T (inspect provider console)", err)
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
