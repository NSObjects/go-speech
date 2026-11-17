//go:build !opencoreamr || !cgo

package amrnb

func Available() bool{return false}
func openNative()(nativeDecoder,error){return nil,ErrUnavailable}
