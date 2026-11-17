package amrnb

import (
    "context"
    "errors"
    "fmt"
    "io"
    "sync"
)

var ErrFramerClosed = errors.New("amrnb: framer input already closed")
var ErrNoFrames = errors.New("amrnb: stream has no frames")

// Sink writes a continuous encoded stream to a persistent decoder.
// audio.DecoderStream satisfies this interface. The Framer does not own the
// Sink; the caller must close the underlying decoder session separately.
type Sink interface {
    Write(ctx context.Context, data []byte)(int,error)
    CloseInput()error
}

// Framer converts successive AMR-NB storage frames into an AMR file byte
// stream by emitting the 6-byte #!AMR header only once, before the first
// frame. It is useful with ffmpeg.OpenStreamDecoder. It does NOT parse RTP.
// WriteFrame and CloseInput are safe to call concurrently (serialized).
type Framer struct {
    mu sync.Mutex
    sink Sink
    started bool
    finished bool
    failed error
}

func NewFramer(sink Sink)(*Framer,error){
    if sink==nil{return nil,fmt.Errorf("%w: nil sink",ErrInvalidFrame)}
    return &Framer{sink:sink},nil
}

func writeAll(ctx context.Context,sink Sink,data []byte)error{
    for len(data)>0{
        if err:=ctx.Err();err!=nil{return err}
        n,err:=sink.Write(ctx,data)
        if n<0 || n>len(data){return io.ErrShortWrite}
        if err!=nil{return err}
        if n==0{return io.ErrShortWrite}
        data=data[n:]
    }
    return nil
}

func (f *Framer) WriteFrame(ctx context.Context,frame []byte)error{
    if f==nil{return ErrFramerClosed}
    if err:=ctx.Err();err!=nil{return err}
    if err:=ValidateFrame(frame);err!=nil{return err}
    f.mu.Lock()
    defer f.mu.Unlock()
    if f.failed!=nil{return f.failed}
    if f.finished{return ErrFramerClosed}
    if !f.started {
        if err:=writeAll(ctx,f.sink,[]byte("#!AMR\n"));err!=nil{
            f.failed=err
            return err
        }
        f.started=true
    }
    if err:=writeAll(ctx,f.sink,frame);err!=nil{
        f.failed=err
        return err
    }
    return nil
}
func (f *Framer) CloseInput()error{
    if f==nil{return ErrFramerClosed}
    f.mu.Lock()
    defer f.mu.Unlock()
    if f.failed!=nil{return f.failed}
    if f.finished{return nil}
    if !f.started{return ErrNoFrames}
    f.finished=true
    return f.sink.CloseInput()
}
