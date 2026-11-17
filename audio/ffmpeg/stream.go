package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/NSObjects/go-speech/audio"
)

var (
	ErrStreamClosed      = errors.New("ffmpeg: streaming decoder is closed")
	ErrStreamInputClosed = errors.New("ffmpeg: streaming decoder input is closed")
)

// StreamConfig configures a long-lived FFmpeg decoding subprocess.
// Input describes a single continuous encoded stream. For Opus and Speex,
// supply Ogg container bytes starting at the Ogg header; AMR means AMR-NB
// with a single #!AMR header. Output MUST be raw PCM16LE. Binary defaults
// to "ffmpeg" on PATH. No FFmpeg process is created until OpenStreamDecoder.
//
// Unlike complete-buffer Converter, this process persists across audio
// packets. It is not a pure-Go decoder; production deployments must provide
// a compatible ffmpeg executable.
type StreamConfig struct {
	Input  audio.Format
	Output audio.Format
	Binary string
}

// StreamDecoder streams compressed input through one FFmpeg subprocess and
// exposes raw PCM output via io-style reads. A single writer and a single
// reader may operate concurrently; do not call Write or Read concurrently
// with themselves. Close may be called from any goroutine, including during
// an in-flight Write/Read, and is idempotent.
type StreamDecoder struct {
	command    *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	cancel     context.CancelFunc
	parent     context.Context
	stopParent func() bool
	stderr     limitedStderr
	format     audio.Format

	writeMu     sync.Mutex
	readMu      sync.Mutex
	inputClosed bool  // guarded by writeMu
	terminalErr error // guarded by readMu

	closeOnce sync.Once
	waitOnce  sync.Once
	waitErr   error // published by waitOnce
	closed    atomic.Bool
}

var _ audio.DecoderStream = (*StreamDecoder)(nil)

// OpenStreamDecoder starts a persistent FFmpeg process. It does not decode any
// audio until Write receives input; Read can then run concurrently. On context
// cancellation the child process is killed and reaped, even if callers are
// blocked reading or writing.
func OpenStreamDecoder(ctx context.Context, cfg StreamConfig) (*StreamDecoder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cfg.Input.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.Output.Validate(); err != nil {
		return nil, err
	}
	if cfg.Output.Encoding != audio.EncodingPCM16LE {
		return nil, fmt.Errorf("%w: stream output requires pcm_s16le", audio.ErrUnsupportedConversion)
	}
	inputFormat, err := inputName(cfg.Input.Encoding)
	if err != nil {
		return nil, err
	}
	name := cfg.Binary
	if name == "" {
		name = "ffmpeg"
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", inputFormat}
	if cfg.Input.Encoding == audio.EncodingPCM16LE {
		args = append(args, "-ar", fmt.Sprint(cfg.Input.SampleRate), "-ac", fmt.Sprint(cfg.Input.Channels))
	}
	args = append(args,
		"-i", "pipe:0", "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-ac", fmt.Sprint(cfg.Output.Channels), "-ar", fmt.Sprint(cfg.Output.SampleRate),
		"-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
	runCtx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(runCtx, name, args...)
	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("ffmpeg: stdin pipe: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return nil, fmt.Errorf("ffmpeg: stdout pipe: %w", err)
	}
	decoder := &StreamDecoder{
		command: command, stdin: stdin, stdout: stdout, cancel: cancel,
		parent: ctx, format: cfg.Output,
	}
	command.Stderr = &decoder.stderr
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		cancel()
		return nil, fmt.Errorf("ffmpeg: start streaming decoder: %w", err)
	}
	decoder.stopParent = context.AfterFunc(ctx, func() { _ = decoder.Close() })
	return decoder, nil
}

// OutputFormat describes the actual decoded PCM16LE bytes returned by Read.
func (d *StreamDecoder) OutputFormat() audio.Format {
	if d == nil {
		return audio.Format{}
	}
	return d.format
}

// Write pushes consecutive encoded bytes into the FFmpeg decoder. The caller
// owns the byte slice and may reuse it after Write returns. A canceled operation
// context aborts the entire decoding session, not just the individual Write.
// Write after CloseInput returns ErrStreamInputClosed.
func (d *StreamDecoder) Write(ctx context.Context, b []byte) (int, error) {
	if d == nil || d.closed.Load() {
		return 0, ErrStreamClosed
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := d.parent.Err(); err != nil {
		return 0, err
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if d.inputClosed {
		return 0, ErrStreamInputClosed
	}
	if d.closed.Load() {
		return 0, ErrStreamClosed
	}
	if len(b) == 0 {
		return 0, nil
	}
	stop := context.AfterFunc(ctx, func() { _ = d.Close() })
	defer stop()
	n, err := d.stdin.Write(b)
	if ctx.Err() != nil {
		return n, ctx.Err()
	}
	if d.parent.Err() != nil {
		return n, d.parent.Err()
	}
	if err != nil {
		return n, fmt.Errorf("ffmpeg: write encoded audio: %w", err)
	}
	return n, nil
}

// CloseInput is idempotent and flushes the decoder by closing stdin.
// The caller must continue draining Read until io.EOF (or an error).
func (d *StreamDecoder) CloseInput() error {
	if d == nil || d.closed.Load() {
		return ErrStreamClosed
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	if d.inputClosed {
		return nil
	}
	if d.closed.Load() {
		return ErrStreamClosed
	}
	d.inputClosed = true
	if err := d.stdin.Close(); err != nil {
		return fmt.Errorf("ffmpeg: close decoder input: %w", err)
	}
	return nil
}

// Read consumes raw PCM16LE bytes. It is valid for a Read to block until
// FFmpeg has enough complete encoded frames for decoding; another goroutine
// should continue writing. An operation context timeout aborts the session.
// A successful EOF means that FFmpeg flushed output and exited successfully.
func (d *StreamDecoder) Read(ctx context.Context, b []byte) (int, error) {
	if d == nil {
		return 0, ErrStreamClosed
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := d.parent.Err(); err != nil {
		return 0, err
	}
	d.readMu.Lock()
	defer d.readMu.Unlock()
	if d.terminalErr != nil {
		return 0, d.terminalErr
	}
	if d.closed.Load() {
		return 0, ErrStreamClosed
	}
	if len(b) == 0 {
		return 0, nil
	}
	stop := context.AfterFunc(ctx, func() { _ = d.Close() })
	defer stop()
	n, err := d.stdout.Read(b)
	if ctx.Err() != nil {
		return n, ctx.Err()
	}
	if d.parent.Err() != nil {
		return n, d.parent.Err()
	}
	if d.closed.Load() {
		return n, ErrStreamClosed
	}
	if errors.Is(err, io.EOF) {
		exitErr := d.wait()
		if exitErr != nil {
			d.terminalErr = exitErr
		} else {
			d.terminalErr = io.EOF
		}
		if n > 0 {
			return n, nil
		}
		return 0, d.terminalErr
	}
	if err != nil {
		d.terminalErr = fmt.Errorf("ffmpeg: read decoded audio: %w", err)
	}
	return n, err
}

func (d *StreamDecoder) wait() error {
	d.waitOnce.Do(func() {
		if err := d.command.Wait(); err != nil {
			d.waitErr = fmt.Errorf("ffmpeg: decoder exited: %w: %s", err, d.stderr.String())
		}
	})
	return d.waitErr
}

// Close aborts any outstanding work, releases pipes and reaps the process.
// It is safe and idempotent; it does not wait for a graceful decoder flush.
// Call CloseInput followed by Read to io.EOF first when all audio is required.
func (d *StreamDecoder) Close() error {
	if d == nil {
		return nil
	}
	d.closeOnce.Do(func() {
		d.closed.Store(true)
		if d.stopParent != nil {
			d.stopParent()
		}
		d.cancel()
		_ = d.stdin.Close()
		_ = d.stdout.Close()
		_ = d.wait()
	})
	return nil
}
