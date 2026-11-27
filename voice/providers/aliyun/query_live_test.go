//go:build integration

package aliyun

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestAliyunLiveVoiceQueries(t *testing.T) {
	c, err := New(Config{APIKey: os.Getenv("DASHSCOPE_API_KEY"), Endpoint: os.Getenv("DASHSCOPE_VOICE_ENDPOINT")})
	if err != nil {
		t.Fatal("invalid Aliyun voice client configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	page, size := 0, 10
	r, err := c.List(ctx, ListRequest{PageIndex: &page, PageSize: &size})
	if err != nil {
		t.Fatalf("list failed: %T (inspect provider console)", err)
	}
	if r.RequestID == "" || len(r.Voices) > size {
		t.Fatal("invalid list response")
	}
	t.Logf("first page: %d voice resources", len(r.Voices))
	t.Run("query", func(t *testing.T) {
		id := os.Getenv("DASHSCOPE_VOICE_ID")
		if id == "" && len(r.Voices) != 0 {
			id = r.Voices[0].VoiceID
		}
		if id == "" {
			t.Skip("NOT VALIDATED: no existing custom voice available")
		}
		v, err := c.Query(ctx, id)
		if err != nil {
			t.Fatalf("query failed: %T (inspect provider console)", err)
		}
		if v.RequestID == "" || v.VoiceID != id || v.Status == "" {
			t.Fatal("invalid voice detail response")
		}
		t.Logf("voice detail succeeded: state=%s", v.State())
	})
}
