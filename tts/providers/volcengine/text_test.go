package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/NSObjects/go-speech/tts"
)

func TestNativeTextFormats(t *testing.T) {
	for _, tc := range []struct {
		format tts.TextFormat
		text   string
		ssml   string
	}{
		{tts.TextPlain, "hello", ""},
		{tts.TextInlineTag, "[happy] hello", ""},
		{tts.TextSSML, "", "<speak>hello</speak>"},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			client := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				for key, want := range map[string]string{"text": tc.text, "ssml": tc.ssml} {
					value, ok := body[key]
					if want == "" {
						if ok {
							t.Errorf("unexpected field %s", key)
						}
						continue
					}
					var got string
					if err := json.Unmarshal(value, &got); err != nil || got != want {
						t.Errorf("%s = %q, %v; want %q", key, got, err, want)
					}
				}
				_, _ = fmt.Fprintln(w, readyJSON)
				_, _ = fmt.Fprintln(w, audioJSON)
				_, _ = fmt.Fprintln(w, finalJSON)
			}))
			result, err := client.Synthesize(context.Background(), tts.Request{
				Text: tc.text + tc.ssml, Voice: fixtureVoice, TextFormat: tc.format,
			})
			if err != nil || len(result.Data) != 3 {
				t.Fatalf("synthesize: %v", err)
			}
		})
	}
}

func TestDuplexRejectsSSMLBeforeConnect(t *testing.T) {
	client := fixtureClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected request")
		w.WriteHeader(http.StatusBadRequest)
	}))
	_, err := client.OpenDuplex(context.Background(), tts.DuplexRequest{
		Voice: fixtureVoice, TextFormat: tts.TextSSML,
	})
	if !errors.Is(err, tts.ErrUnsupportedTextFormat) {
		t.Fatalf("expected unsupported text format, got %v", err)
	}
}
