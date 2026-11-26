package tts

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/speecherr"
)

type detailedTestClient struct {
	stream Stream
	err    error
	req    Request
	calls  int
}

func (c *detailedTestClient) SynthesizeStream(_ context.Context, req Request) (Stream, error) {
	c.req = req
	c.calls++
	return c.stream, c.err
}

type audioOnlyTestStream struct {
	closed int
}

func (s *audioOnlyTestStream) Recv(context.Context) (audio.Chunk, error) {
	return audio.Chunk{}, errors.New("unexpected audio-only read")
}

func (s *audioOnlyTestStream) Close() error {
	s.closed++
	return nil
}

type detailedTestStream struct {
	audioOnlyTestStream
	events []Event
	endErr error
	index  int
}

func (s *detailedTestStream) RecvEvent(context.Context) (Event, error) {
	if s.index == len(s.events) {
		if s.endErr != nil {
			return Event{}, s.endErr
		}
		return Event{}, io.EOF
	}
	e := s.events[s.index]
	s.index++
	return e, nil
}

func TestSynthesizeDetailed(t *testing.T) {
	format := audio.Format{Encoding: audio.EncodingMP3, SampleRate: 24000, Channels: 1}
	word := Word{Text: "你", EndTime: 200 * time.Millisecond}
	s := &detailedTestStream{events: []Event{
		{Type: EventAudio, Audio: &audio.Chunk{Data: []byte("a"), Format: format}},
		{Type: EventBatchCompleted},
		{Type: EventAlignment, Alignment: &Alignment{Text: "你", Words: []Word{word}, Provider: "native"}},
		{Type: EventAudio, Audio: &audio.Chunk{Data: []byte("b"), Format: format}},
		{Type: EventAlignment, Alignment: &Alignment{Text: "好"}},
	}}
	c := &detailedTestClient{stream: s}
	req := Request{Voice: "voice", Text: "你好", TextFormat: TextInlineTag}
	out, err := SynthesizeDetailed(context.Background(), c, req)
	if err != nil {
		t.Fatal(err)
	}
	if c.calls != 1 || c.req.Text != req.Text || c.req.TextFormat != req.TextFormat || s.closed != 1 {
		t.Fatalf("request or lifecycle mismatch: %+v, closed=%d", c, s.closed)
	}
	if string(out.Audio.Data) != "ab" || out.Audio.Format != format ||
		len(out.Alignments) != 2 || len(out.Alignments[0].Words) != 1 ||
		out.Alignments[0].Words[0] != word || out.Alignments[0].Provider != "native" ||
		out.Alignments[1].Text != "好" {
		t.Fatalf("result = %+v", out)
	}
}

func TestDetailedFailuresCloseStream(t *testing.T) {
	remote := errors.New("remote failure")
	format := audio.Format{Encoding: audio.EncodingMP3, SampleRate: 24000, Channels: 1}
	valid := Event{Type: EventAudio, Audio: &audio.Chunk{Data: []byte("a"), Format: format}}
	for _, tc := range []struct {
		name   string
		events []Event
		endErr error
		code   speecherr.Code
	}{
		{name: "empty", code: speecherr.CodeProtocol},
		{name: "remote error", events: []Event{valid}, endErr: remote},
		{name: "nil audio", events: []Event{{Type: EventAudio}}, code: speecherr.CodeProtocol},
		{name: "nil alignment", events: []Event{valid, {Type: EventAlignment}}, code: speecherr.CodeProtocol},
		{name: "format change", events: []Event{valid, {Type: EventAudio, Audio: &audio.Chunk{Data: []byte("b")}}}, code: speecherr.CodeProtocol},
		{name: "unknown event", events: []Event{valid, {Type: "unknown"}}, code: speecherr.CodeProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &detailedTestStream{events: tc.events, endErr: tc.endErr}
			out, err := SynthesizeDetailed(context.Background(), &detailedTestClient{stream: s}, Request{})
			if err == nil || (tc.code != "" && !speecherr.IsCode(err, tc.code)) ||
				(tc.endErr != nil && !errors.Is(err, tc.endErr)) || len(out.Audio.Data) != 0 ||
				len(out.Alignments) != 0 || s.closed != 1 {
				t.Fatalf("out=%+v err=%v closed=%d", out, err, s.closed)
			}
		})
	}
	s := &audioOnlyTestStream{}
	_, err := SynthesizeDetailed(context.Background(), &detailedTestClient{stream: s}, Request{})
	if !errors.Is(err, ErrEventsUnsupported) || !speecherr.IsCode(err, speecherr.CodeUnsupported) || s.closed != 1 {
		t.Fatalf("unsupported: %v, closed=%d", err, s.closed)
	}
}

func TestDetailedLimitsAndCancellation(t *testing.T) {
	s := &detailedTestStream{events: []Event{
		{Type: EventAudio, Audio: &audio.Chunk{Data: []byte("ab")}},
		{Type: EventAudio, Audio: &audio.Chunk{Data: []byte("c")}},
	}}
	if _, err := collectDetailed(context.Background(), s, 2); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatalf("audio limit: %v", err)
	}
	s = &detailedTestStream{events: []Event{
		{Type: EventAlignment, Alignment: &Alignment{Words: make([]Word, maxDetailedWords+1)}},
	}}
	if _, err := collectDetailed(context.Background(), s, 2); !speecherr.IsCode(err, speecherr.CodeInvalidArgument) {
		t.Fatalf("word limit: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &detailedTestClient{}
	if _, err := SynthesizeDetailed(ctx, c, Request{}); !errors.Is(err, context.Canceled) || c.calls != 0 {
		t.Fatalf("canceled request: %v, calls=%d", err, c.calls)
	}
}
