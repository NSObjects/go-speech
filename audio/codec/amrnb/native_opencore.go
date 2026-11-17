//go:build opencoreamr && cgo

package amrnb

/*
#cgo pkg-config: opencore-amrnb
#include <opencore-amrnb/interf_dec.h>
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"unsafe"
)

type openCoreDecoder struct{ ptr unsafe.Pointer }

func Available() bool { return true }
func openNative() (nativeDecoder, error) {
	ptr := C.Decoder_Interface_init()
	if ptr == nil {
		return nil, errors.New("amrnb: OpenCORE decoder initialization failed")
	}
	return &openCoreDecoder{ptr: ptr}, nil
}
func (n *openCoreDecoder) decode(frame []byte) ([]byte, error) {
	samples := make([]C.short, 160)
	var bfi C.int
	if frame[0]&0x04 == 0 {
		bfi = 1
	}
	C.Decoder_Interface_Decode(n.ptr,
		(*C.uchar)(unsafe.Pointer(&frame[0])),
		&samples[0], bfi)
	out := make([]byte, 320)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(sample)))
	}
	return out, nil
}
func (n *openCoreDecoder) close() {
	if n.ptr != nil {
		C.Decoder_Interface_exit(n.ptr)
		n.ptr = nil
	}
}
