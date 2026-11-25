// Command tts-switch demonstrates the public TTS API without importing
// provider implementation packages. Voice IDs remain provider-specific.
//
// Example:
//
//	TTS_PROVIDER=volcengine VOLCENGINE_API_KEY=... TTS_VOICE=your-seed3-voice-id \
//	  go run ./examples/tts-switch '你好，欢迎使用语音合成。' output.mp3
//
// Set TTS_MODE=batch for one-shot synthesis; streaming is the default.
// Output is MP3 at 24 kHz mono; the SDK never converts formats.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	speech "github.com/NSObjects/go-speech"
	"github.com/NSObjects/go-speech/audio"
	"github.com/NSObjects/go-speech/tts"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func configFor(name string) (speech.TTSConfig, error) {
	switch name {
	case "aliyun":
		return speech.AliyunTTS{
			APIKey:   os.Getenv("DASHSCOPE_API_KEY"),
			Endpoint: os.Getenv("DASHSCOPE_WS_ENDPOINT"),
		}, nil
	case "tencent":
		return speech.TencentTTS{
			AppID:     os.Getenv("TENCENT_APP_ID"),
			SecretID:  os.Getenv("TENCENT_SECRET_ID"),
			SecretKey: os.Getenv("TENCENT_SECRET_KEY"),
		}, nil
	case "volcengine":
		return speech.VolcengineTTS{
			APIKey:  os.Getenv("VOLCENGINE_API_KEY"),
			ModelID: os.Getenv("VOLCENGINE_TTS_MODEL_ID"),
		}, nil
	default:
		return nil, errors.New("TTS_PROVIDER must be aliyun, tencent or volcengine")
	}
}

func run() error {
	if len(os.Args) != 3 {
		return errors.New("usage: go run ./examples/tts-switch <text> <output.mp3>")
	}
	voice := os.Getenv("TTS_VOICE")
	if voice == "" {
		return errors.New("TTS_VOICE must be a voice ID supported by the selected provider")
	}
	cfg, err := configFor(os.Getenv("TTS_PROVIDER"))
	if err != nil {
		return err
	}
	client, err := speech.NewTTS(cfg)
	if err != nil {
		return fmt.Errorf("configure TTS: %w", err)
	}
	mode := os.Getenv("TTS_MODE")
	if mode == "" {
		mode = "stream"
	}
	if mode != "stream" && mode != "batch" {
		return errors.New("TTS_MODE must be stream or batch")
	}
	request := tts.Request{
		Text:  os.Args[1],
		Voice: voice,
		Format: audio.Format{
			Encoding: audio.EncodingMP3, SampleRate: 24000, Channels: 1,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// A failed stream must not leave an incomplete file at the destination.
	outputPath := os.Args[2]
	tmp, err := os.CreateTemp(filepath.Dir(outputPath), ".tts-*.part")
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	var written int64
	if mode == "batch" {
		buffer, err := client.Synthesize(ctx, request)
		if err != nil {
			return err
		}
		if buffer.Format != request.Format {
			return fmt.Errorf("unexpected output audio format: %+v", buffer.Format)
		}
		n, err := tmp.Write(buffer.Data)
		written += int64(n)
		if err != nil {
			return err
		}
	} else {
		stream, err := client.SynthesizeStream(ctx, request)
		if err != nil {
			return err
		}
		defer stream.Close()
		for {
			chunk, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if chunk.Format != request.Format {
				return fmt.Errorf("unexpected output audio format: %+v", chunk.Format)
			}
			n, err := tmp.Write(chunk.Data)
			written += int64(n)
			if err != nil {
				return err
			}
		}
	}
	if written == 0 {
		return errors.New("TTS completed without audio")
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), outputPath); err != nil {
		return err
	}
	fmt.Printf("TTS: %s audio written to %s (%d bytes)\n", mode, outputPath, written)
	return nil
}
