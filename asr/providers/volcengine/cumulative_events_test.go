package volcengine

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/NSObjects/go-speech/asr"
)

// Full-mode snapshots can repeatedly contain every previously finalized
// utterance. The adapter must emit precisely one final per new segment even
// after a long series of cumulative updates on the same session.
func TestManyCumulativeUtterancesEmitExactlyOnce(t *testing.T) {
	const count = 32
	stream := &Stream{resultType: ResultFull, finalSeen: make(map[string]struct{})}
	var history []utterance
	for i := 0; i < count; i++ {
		item := utterance{
			Text:     fmt.Sprintf("第%d句", i+1),
			Definite: true,
			Start:    int64(i * 2000),
			End:      int64(i*2000 + 800),
		}
		history = append(history, item)
		transcript := recognition{Utterances: append([]utterance(nil), history...)}
		stream.addEvents([]recognition{transcript}, false)
		want := []asr.Event{{Type: asr.EventFinal, Text: item.Text}}
		if !reflect.DeepEqual(stream.queue, want) {
			t.Fatalf("step %d: events=%#v, want=%#v", i, stream.queue, want)
		}
		stream.queue = nil
		// Server replays the same cumulative snapshot. No new final event.
		stream.addEvents([]recognition{transcript}, false)
		if len(stream.queue) > 0 {
			t.Fatalf("step %d: repeated snapshot yielded %#v", i, stream.queue)
		}
	}
	stream.addEvents([]recognition{{Utterances: history}}, true)
	if len(stream.queue) != 0 {
		t.Fatalf("terminal cumulative response repeated finals: %#v", stream.queue)
	}
	if len(stream.finalSeen) != count {
		t.Fatalf("distinct final count=%d, expected=%d", len(stream.finalSeen), count)
	}
}
