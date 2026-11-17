//go:build !libopus || !cgo

package opus

func openNative(sampleRate, channels int) (nativeDecoder, error) {
	return nil, ErrUnavailable
}

// Available reports whether the module was built with the optional native
// libopus binding. This is false in default (pure-Go) builds.
func Available() bool { return false }
