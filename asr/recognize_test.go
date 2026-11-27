package asr

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
)

type fakeShortRecognizer struct {
	stream *fakeShortStream
	err    error
	cfg    Config
}

func (c *fakeShortRecognizer) OpenStream(ctx context.Context, cfg Config) (Stream, error) {
	c.cfg = cfg
	if c.err != nil {
		return nil, c.err
	}
	return c.stream, nil
}

type fakeShortStream struct {
	mu        sync.Mutex
	audio     []byte
	sent      int
	closed    bool
	closeSend bool
	events    chan Event
	done      chan struct{}
	once      sync.Once
	sendErr   error
	recvErr   error
}

func newFakeStream() *fakeShortStream {
	return &fakeShortStream{events: make(chan Event, 8), done: make(chan struct{})}
}
func (s *fakeShortStream) Send(ctx context.Context, b []byte) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrStreamClosed
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audio = append(s.audio, b...)
	s.sent++
	return nil
}
func (s *fakeShortStream) CloseSend(ctx context.Context) error {
	s.mu.Lock()
	s.closeSend = true
	s.mu.Unlock()
	close(s.events)
	return nil
}
func (s *fakeShortStream) Recv(ctx context.Context) (Event, error) {
	if s.recvErr != nil {
		return Event{}, s.recvErr
	}
	select {
	case <-ctx.Done():
		return Event{}, ctx.Err()
	case <-s.done:
		return Event{}, ErrStreamClosed
	case e, ok := <-s.events:
		if !ok {
			return Event{}, io.EOF
		}
		return e, nil
	}
}
func (s *fakeShortStream) Close() error {
	s.once.Do(func() { s.mu.Lock(); s.closed = true; s.mu.Unlock(); close(s.done) })
	return nil
}
func validBuffer(size int) audio.Buffer {
	return audio.Buffer{Data: []byte(strings.Repeat("a", size)), Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1}}
}
func TestRecognizeStreamAggregatesFinalEventsAndSendsChunks(t *testing.T) {
	s := newFakeStream()
	s.events <- Event{Type: EventPartial, Text: "wrong"}
	s.events <- Event{Type: EventFinal, Text: "你好，", Language: "zh-CN"}
	s.events <- Event{Type: EventFinal, Text: "世界。"}
	c := &fakeShortRecognizer{stream: s}
	input := validBuffer(shortAudioChunkBytes*2 + 2)
	input.Format.Encoding = audio.EncodingMP3
	got, err := RecognizeStream(context.Background(), c, input, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "你好，世界。" || got.Language != "zh-CN" {
		t.Fatalf("result: %+v", got)
	}
	if c.cfg.Format != input.Format {
		t.Fatalf("format: %+v", c.cfg.Format)
	}
	if !s.closeSend || s.sent != 3 || len(s.audio) != len(input.Data) {
		t.Fatalf("upload complete=%v chunks=%d bytes=%d", s.closeSend, s.sent, len(s.audio))
	}
	if !s.closed {
		t.Fatal("session must be closed")
	}
}
func TestRecognizeStreamPCMUsesPacedFrames(t *testing.T) {
	s := newFakeStream()
	s.events <- Event{Type: EventFinal, Text: "你好"}
	c := &fakeShortRecognizer{stream: s}
	input := validBuffer(16000 * 2 / 25 * 2)
	start := time.Now()
	got, err := RecognizeStream(context.Background(), c, input, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "你好" {
		t.Fatalf("got %+v", got)
	}
	if s.sent != 2 {
		t.Fatalf("expected two 40ms PCM frames, got %d", s.sent)
	}
	if time.Since(start) < 65*time.Millisecond {
		t.Fatal("PCM was not paced at realtime speed")
	}
}

func TestRecognizeStreamRejectsInvalidInputBeforeOpening(t *testing.T) {
	for _, test := range []struct {
		name  string
		input audio.Buffer
		cfg   Config
	}{
		{"empty", validBuffer(0), Config{}},
		{"oversized", validBuffer(MaxShortAudioBytes + 1), Config{}},
		{"invalid format", audio.Buffer{Data: []byte("audio")}, Config{}},
		{"format mismatch", validBuffer(2), Config{Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 8000, Channels: 1}}},
		{"bad PCM frame", validBuffer(3), Config{}},
		{"too long PCM", validBuffer(16000 * 2 * 61), Config{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := &fakeShortRecognizer{err: errors.New("should not open")}
			_, err := RecognizeStream(context.Background(), c, test.input, test.cfg)
			if !errors.Is(err, ErrInvalidShortAudio) {
				t.Fatalf("want ErrInvalidShortAudio, got %v", err)
			}
		})
	}
}
func TestRecognizeStreamErrors(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		expected := errors.New("open error")
		_, err := RecognizeStream(context.Background(), &fakeShortRecognizer{err: expected}, validBuffer(2), Config{})
		if !errors.Is(err, expected) {
			t.Fatal(err)
		}
	})
	t.Run("send", func(t *testing.T) {
		expected := errors.New("send error")
		s := newFakeStream()
		s.sendErr = expected
		_, err := RecognizeStream(context.Background(), &fakeShortRecognizer{stream: s}, validBuffer(2), Config{})
		if !errors.Is(err, expected) {
			t.Fatal(err)
		}
	})
	t.Run("recv", func(t *testing.T) {
		expected := errors.New("recv error")
		s := newFakeStream()
		s.recvErr = expected
		_, err := RecognizeStream(context.Background(), &fakeShortRecognizer{stream: s}, validBuffer(2), Config{})
		if !errors.Is(err, expected) {
			t.Fatal(err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := RecognizeStream(ctx, &fakeShortRecognizer{err: errors.New("should not open")}, validBuffer(2), Config{})
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		<-ctx.Done()
		_, err := RecognizeStream(ctx, &fakeShortRecognizer{err: errors.New("should not open")}, validBuffer(2), Config{})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
}

func TestRecognizePreservesEmptyFinalMetadata(t *testing.T) {
	s := newFakeStream()
	s.events <- Event{Type: EventFinal, Text: "你好", Language: "zh", Utterance: &Utterance{ID: "a", Text: "你好"}}
	s.events <- Event{Type: EventFinal, Language: "en", Utterance: &Utterance{ID: "b", Definite: true}}
	s.events <- Event{Type: EventUtteranceUpdate, Utterance: &Utterance{ID: "b", Definite: true, Emotion: "neutral"}}
	s.events <- Event{Type: EventFinal}
	s.events <- Event{Type: EventFinal, Text: "再见", Utterance: &Utterance{Text: "再见"}}
	got, err := RecognizeStream(context.Background(), &fakeShortRecognizer{stream: s}, validBuffer(2), Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "你好再见" || got.Language != "zh" || len(got.Utterances) != 3 {
		t.Fatalf("unexpected result: %+v", got)
	}
	empty := got.Utterances[1]
	if empty.ID != "b" || empty.Text != "" || !empty.Definite || empty.Emotion != "neutral" {
		t.Fatalf("empty final metadata lost: %+v", empty)
	}
}
