package audio

import (
    "errors"
    "fmt"
)

// ErrUnsupportedFormat means an audio format is not accepted by a provider or
// model. A structurally valid Format may still be unsupported by a service.
var ErrUnsupportedFormat = errors.New("audio: unsupported format")

// Capability declares what a particular model can accept. Empty SampleRates
// or Channels means unrestricted within the shared Format contract.
// Encodings must be explicit; a model with zero encodings supports nothing.
type Capability struct {
    Encodings   []Encoding
    SampleRates []int
    Channels    []int
}

func containsEncoding(list []Encoding,v Encoding) bool {
    for _,item:=range list{if item==v{return true}}
    return false
}
func containsInt(list []int,v int) bool {
    if len(list)==0{return true}
    for _,item:=range list{if item==v{return true}}
    return false
}

// Validate rejects formats that the model does not support; it does not
// inspect audio bytes, decode compressed streams, or resample input.
func (c Capability) Validate(f Format) error {
    if err:=f.Validate();err!=nil{return err}
    if !containsEncoding(c.Encodings,f.Encoding){
        return fmt.Errorf("%w: encoding %q",ErrUnsupportedFormat,f.Encoding)
    }
    if !containsInt(c.SampleRates,f.SampleRate){
        return fmt.Errorf("%w: %d Hz",ErrUnsupportedFormat,f.SampleRate)
    }
    if !containsInt(c.Channels,f.Channels){
        return fmt.Errorf("%w: %d channels",ErrUnsupportedFormat,f.Channels)
    }
    return nil
}
func (c Capability) Supports(f Format) bool {return c.Validate(f)==nil}
