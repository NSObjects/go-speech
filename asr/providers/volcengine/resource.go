package volcengine

import "time"

// Known resource IDs identify the model generation, independent of billing.
const (
	resourceASR1Duration   = "volc.bigasr.sauc.duration"
	resourceASR1Concurrent = "volc.bigasr.sauc.concurrent"
	minSeedEndWindow       = 300 * time.Millisecond
	maxSeedEndWindow       = 5000 * time.Millisecond
	minLegacyEndWindow     = 200 * time.Millisecond
)

func isLegacyResource(resource string) bool {
	return resource == resourceASR1Duration || resource == resourceASR1Concurrent
}

func isSeedResource(resource string) bool {
	return resource == "" || resource == ResourceSeedASR2 || resource == ResourceSeedASR2Concurrent
}

// The endpoint-specific reference updated 2026-11-18 gives 300–5000 ms.
// The older combined API reference gives a 200 ms minimum. Preserve the
// historical lower-bound behavior for explicitly selected legacy resources
// and unrecognized resource IDs, rather than guessing their capabilities.
func validateEndWindow(resource string, window time.Duration) error {
	if window == 0 {
		return nil
	}
	if window%time.Millisecond != 0 {
		return argumentError("EndWindowSize must be a whole number of milliseconds")
	}
	if isSeedResource(resource) {
		if window < minSeedEndWindow || window > maxSeedEndWindow {
			return argumentError("EndWindowSize must be within [300, 5000] ms for Seed-ASR 2.0")
		}
	} else if window < minLegacyEndWindow {
		return argumentError("EndWindowSize must be at least 200ms")
	}
	return nil
}
