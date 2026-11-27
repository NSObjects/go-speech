package volcengine

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/speecherr"
	"github.com/NSObjects/go-speech/tts"
)

func TestHTTPErrorBodyReadIsBounded(t *testing.T) {
	c := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	c.cfg.StartTimeout = 250 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := c.SynthesizeStream(ctx, tts.Request{Text: "hello", Voice: fixtureVoice})
	if !speecherr.IsCode(err, speecherr.CodeUnauthorized) {
		t.Fatalf("stalled diagnostic response: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("diagnostic read exhausted caller deadline")
	}
}
