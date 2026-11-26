package tts

import "errors"

// ErrUnsupportedTextFormat indicates a text syntax unavailable in the selected mode.
var ErrUnsupportedTextFormat = errors.New("tts: unsupported text format")

// TextFormat selects the native text syntax of a synthesis request.
// Its zero value preserves the provider's existing plain-text behavior.
// Markup is not translated between providers. The selected model and voice
// determine the supported tags.
type TextFormat string

const (
	TextPlain     TextFormat = "plain"
	TextSSML      TextFormat = "ssml"
	TextInlineTag TextFormat = "inline_tag"
)
