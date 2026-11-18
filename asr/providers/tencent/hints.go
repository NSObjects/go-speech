package tencent

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// encodeHints maps the provider-independent per-stream Hints to Tencent's
// temporary hotword_list. Tencent requires "word|weight" entries; a moderate
// default weight avoids forcing the transcript to match a hint.
func encodeHints(hints []string) (string, error) {
	if len(hints) == 0 {
		return "", nil
	}
	if len(hints) > 128 {
		return "", requestError("Tencent accepts at most 128 temporary hotwords")
	}
	const weight = "|5"
	seen := make(map[string]struct{}, len(hints))
	entries := make([]string, 0, len(hints))
	for _, hint := range hints {
		if hint == "" || utf8.RuneCountInString(hint) > 30 || !utf8.ValidString(hint) {
			return "", requestError("Tencent hotwords must contain 1-30 valid Unicode characters")
		}
		han := 0
		for _, r := range hint {
			if unicode.Is(unicode.Han, r) {
				han++
			}
			if unicode.IsSpace(r) || unicode.IsControl(r) || r == '|' || r == ',' {
				return "", requestError("Tencent hotwords cannot contain whitespace or list delimiters")
			}
		}
		if han > 10 {
			return "", requestError("Tencent hotwords may contain at most 10 Han characters")
		}
		if _, ok := seen[hint]; ok {
			continue
		}
		seen[hint] = struct{}{}
		entries = append(entries, hint+weight)
	}
	return strings.Join(entries, ","), nil
}
