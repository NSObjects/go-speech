// Command asr-switch demonstrates selecting an ASR provider by one
// application-owned environment variable without importing provider packages.
//
// Usage:
//
//	ASR_PROVIDER=tencent TENCENT_APP_ID=... TENCENT_SECRET_ID=... TENCENT_SECRET_KEY=... \
//	  go run ./examples/asr-switch speech.pcm
//
// speech.pcm must be raw 16 kHz mono signed PCM16LE, not WAV.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	speech "github.com/NSObjects/go-speech"
	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: go run ./examples/asr-switch <16k-mono.pcm>")
	}
	// This map is application configuration, not an SDK-level global.
	configs := map[string]speech.ASRConfig{
		"tencent": speech.TencentASR{
			AppID: os.Getenv("TENCENT_APP_ID"), SecretID: os.Getenv("TENCENT_SECRET_ID"),
			SecretKey: os.Getenv("TENCENT_SECRET_KEY"), EngineModel: "16k_zh_en_2.0",
			NeedVAD: true,
		},
		"aliyun": speech.AliyunASR{
			APIKey:   os.Getenv("DASHSCOPE_API_KEY"),
			Endpoint: "wss://dashscope.aliyuncs.com/api-ws/v1/inference",
		},
		"volcengine": speech.VolcengineASR{APIKey: os.Getenv("VOLCENGINE_API_KEY")},
	}
	cfg, ok := configs[os.Getenv("ASR_PROVIDER")]
	if !ok {
		return errors.New("ASR_PROVIDER must be tencent, aliyun or volcengine")
	}
	client, err := speech.NewASR(cfg)
	if err != nil {
		return fmt.Errorf("configure ASR: %w", err)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data)%2 != 0 {
		return errors.New("input must be nonempty raw PCM16LE with complete 16-bit samples")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(len(data))*time.Second/32000+60*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, asr.Config{
		Format: audio.Format{Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1},
	})
	if err != nil {
		return err
	}
	defer stream.Close()

	recvDone := make(chan error, 1)
	go func() {
		for {
			event, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				recvDone <- nil
				return
			}
			if err != nil {
				recvDone <- err
				cancel()
				return
			}
			switch event.Type {
			case asr.EventPartial:
				fmt.Println("Partial:", event.Text)
			case asr.EventFinal:
				fmt.Println("Final:", event.Text)
			}
		}
	}()

	// 40ms PCM chunks preserve approximately real-time pacing for V2.
	const frameBytes = 16000 * 2 * 40 / 1000
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	var sendErr error
sendLoop:
	for offset := 0; offset < len(data); offset += frameBytes {
		if offset > 0 {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				sendErr = ctx.Err()
				break sendLoop
			}
		}
		end := offset + frameBytes
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(ctx, data[offset:end]); err != nil {
			sendErr = err
			break
		}
	}
	if sendErr == nil {
		sendErr = stream.CloseSend(ctx)
	}
	if sendErr != nil {
		cancel()
	}
	recvErr := <-recvDone
	if sendErr != nil {
		return sendErr
	}
	return recvErr
}
