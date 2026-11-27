package asr

// Options contains request-local recognition controls. Nil pointers preserve
// existing defaults. Unsupported options must be rejected, not ignored.
type Options struct {
	Punctuation       *bool
	TextNormalization *bool
	DisfluencyRemoval *bool
	// UtteranceDetails enables available segment and word timing metadata.
	UtteranceDetails bool
	Provider         ProviderOptions
}

// ProviderOptions holds typed native options for the selected adapter.
type ProviderOptions interface {
	ASRProviderOptions()
}
