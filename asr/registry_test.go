package asr

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/NSObjects/go-speech/audio"
)

type fakeRecognizer struct{}

func (fakeRecognizer) Recognize(context.Context, audio.Buffer, Config) (Result, error) {
	return Result{Text: "hello"}, nil
}

func TestRegistry(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(" ", Provider{Batch: fakeRecognizer{}}); !errors.Is(err, ErrInvalidProvider) {
		t.Fatalf("bad name: got %v", err)
	}
	if err := r.Register("x", Provider{}); !errors.Is(err, ErrInvalidProvider) {
		t.Fatalf("missing capability: got %v", err)
	}
	if err := r.Register("b", Provider{Batch: fakeRecognizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("a", Provider{Batch: fakeRecognizer{}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("b", Provider{Batch: fakeRecognizer{}}); !errors.Is(err, ErrProviderExists) {
		t.Fatalf("duplicate: got %v", err)
	}
	if _, ok := r.Lookup("missing"); ok {
		t.Fatal("unexpected provider")
	}
	if p, ok := r.Lookup("b"); !ok || p.Batch == nil || p.Stream != nil {
		t.Fatalf("incorrect provider: %#v, %v", p, ok)
	}
	if names := r.Names(); !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Fatalf("names: %v", names)
	}
}

func TestRegistryConcurrent(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("provider-%02d", i)
			if err := r.Register(name, Provider{Batch: fakeRecognizer{}}); err != nil {
				t.Errorf("register %s: %v", name, err)
				return
			}
			_, _ = r.Lookup(name)
			_ = r.Names()
		}(i)
	}
	wg.Wait()
	if got := len(r.Names()); got != 32 {
		t.Fatalf("got %d providers, want 32", got)
	}
}
