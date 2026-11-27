package volcengine

import "strings"

// Language rules follow Seed-TTS 3.0 language-support (2026-11-23).
// A language code can represent multiple dialects (e.g. 0). The server
// still determines dialect-level support and whether the supplied text
// matches the recording. Unknown positive codes are delegated to the
// service so new languages do not require an SDK upgrade.
func cloneLanguageRules(code int) (promptRequired, boostUnsupported bool) {
	switch {
	case code >= 0 && code <= 20:
		return false, false
	case code == 25 || code == 29 || code == 45 || code == 51 || code == 86:
		return false, false
	case code == 62:
		return false, true // en-GB
	case code >= 21 && code <= 100 || code == 118:
		return true, true
	default:
		return false, false
	}
}

func validateCloneLanguage(code int, opts *CloneOptions) error {
	required, noBoost := cloneLanguageRules(code)
	if required && (opts == nil || strings.TrimSpace(opts.PromptText) == "") {
		return invalid("selected clone language requires prompt text matching the recording")
	}
	if noBoost && opts != nil && opts.ProsodyBoost != nil && *opts.ProsodyBoost {
		return invalid("selected clone language does not support prosody boost")
	}
	return nil
}
