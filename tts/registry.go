package tts

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrInvalidProvider = errors.New("tts: provider name or capability is invalid")
	ErrProviderExists  = errors.New("tts: provider already registered")
)

// Provider groups optional TTS capabilities. At least one must be non-nil.
type Provider struct {
	Batch  Synthesizer
	Stream StreamSynthesizer
}

// Registry stores providers for a single application. It is safe for
// concurrent registration and lookup. No global registry is created.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register adds a provider under a caller-chosen unique name.
func (r *Registry) Register(name string, provider Provider) error {
	if r == nil || strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) ||
		(provider.Batch == nil && provider.Stream == nil) {
		return ErrInvalidProvider
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = make(map[string]Provider)
	}
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("%w: %s", ErrProviderExists, name)
	}
	r.providers[name] = provider
	return nil
}

// Lookup returns the provider registered under name, if any.
func (r *Registry) Lookup(name string) (Provider, bool) {
	if r == nil {
		return Provider{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[name]
	return provider, ok
}

// Names returns sorted provider names.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
