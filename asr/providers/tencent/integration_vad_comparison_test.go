//go:build integration

package tencent

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/asr"
	"github.com/NSObjects/go-speech/audio"
)

// vadComparisonResult is a diagnostic, not a recognition-quality score.
// One stream is opened per threshold to keep the comparison independent.
type vadComparisonResult struct {
	threshold    time.Duration
	partials     int
	finalOffsets []time.Duration
	wallElapsed  time.Duration
}

// trimSyntheticTrailingSilence trims only the terminal zero/very-low-energy
// padding emitted by espeak-ng. It intentionally does not modify speech
// inside the fixture, and preserves a short acoustic tail.
func trimSyntheticTrailingSilence(pcm []byte) ([]byte, error) {
	if len(pcm)%2 != 0 {
		return nil, errors.New("synthetic PCM must contain complete 16-bit samples")
	}
	lastSpeechSample := -1
	for i := 0; i < len(pcm); i += 2 {
		amplitude := int(int16(binary.LittleEndian.Uint16(pcm[i : i+2])))
		if amplitude < 0 {
			amplitude = -amplitude
		}
		if amplitude >= 128 {
			lastSpeechSample = i / 2
		}
	}
	if lastSpeechSample < 1600 {
		return nil, errors.New("synthetic fixture does not contain enough detectable speech")
	}
	// Keep 100ms of tail after the last clearly voiced sample.
	end := (lastSpeechSample + 1 + 1600) * 2
	if end > len(pcm) {
		end = len(pcm)
	}
	return pcm[:end], nil
}

func buildVADComparisonPCM(source []byte) (pcm []byte, firstEnd, secondStart time.Duration, err error) {
	speech, err := trimSyntheticTrailingSilence(source)
	if err != nil {
		return nil, 0, 0, err
	}
	const bytesPerSecond = 16000 * 2
	const pause = 1200 * time.Millisecond
	const finalTail = 2400 * time.Millisecond
	firstEnd = time.Duration(len(speech)) * time.Second / bytesPerSecond
	secondStart = firstEnd + pause
	pauseBytes := int(pause * bytesPerSecond / time.Second)
	tailBytes := int(finalTail * bytesPerSecond / time.Second)
	pcm = make([]byte, 0, 2*len(speech)+pauseBytes+tailBytes)
	pcm = append(pcm, speech...)
	pcm = append(pcm, make([]byte, pauseBytes)...)
	pcm = append(pcm, speech...)
	pcm = append(pcm, make([]byte, tailBytes)...)
	return pcm, firstEnd, secondStart, nil
}

func runTencentVADComparison(ctx context.Context, pcm []byte, threshold time.Duration) (vadComparisonResult, error) {
	out := vadComparisonResult{threshold: threshold}
	client, err := New(Config{
		AppID:          os.Getenv("TENCENT_APP_ID"),
		SecretID:       os.Getenv("TENCENT_SECRET_ID"),
		SecretKey:      os.Getenv("TENCENT_SECRET_KEY"),
		EngineModel:    DefaultEngineModel,
		NeedVAD:        true,
		VADSilenceTime: threshold,
	})
	if err != nil {
		return out, err
	}
	stream, err := client.OpenStream(ctx, asr.Config{Format: audio.Format{
		Encoding: audio.EncodingPCM16LE, SampleRate: 16000, Channels: 1,
	}})
	if err != nil {
		return out, fmt.Errorf("open stream with VAD=%s: %w", threshold, err)
	}
	defer stream.Close()

	type recvResult struct {
		partials int
		finals   []time.Duration
		err      error
	}
	received := make(chan recvResult, 1)
	started := time.Now()
	go func() {
		var result recvResult
		for {
			event, err := stream.Recv(ctx)
			if errors.Is(err, io.EOF) {
				received <- result
				return
			}
			if err != nil {
				result.err = err
				received <- result
				return
			}
			switch event.Type {
			case asr.EventPartial:
				result.partials++
			case asr.EventFinal:
				if event.Text != "" {
					result.finals = append(result.finals, time.Since(started))
				}
			}
		}
	}()

	const frame = 16000 * 2 / 10 // 100ms of PCM16LE mono
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for offset := 0; offset < len(pcm); offset += frame {
		if offset > 0 {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return out, ctx.Err()
			}
		}
		end := offset + frame
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := stream.Send(ctx, pcm[offset:end]); err != nil {
			return out, fmt.Errorf("VAD %s PCM send: %w", threshold, err)
		}
	}
	if err := stream.CloseSend(ctx); err != nil {
		return out, fmt.Errorf("VAD %s finish: %w", threshold, err)
	}
	select {
	case result := <-received:
		if result.err != nil {
			return out, fmt.Errorf("VAD %s receive: %w", threshold, result.err)
		}
		if len(result.finals) == 0 {
			return out, fmt.Errorf("VAD %s returned no non-empty Final event", threshold)
		}
		out.partials = result.partials
		out.finalOffsets = result.finals
		out.wallElapsed = time.Since(started)
		if _, err := stream.Recv(context.Background()); !errors.Is(err, io.EOF) {
			return out, fmt.Errorf("VAD %s failed stable terminal EOF: %v", threshold, err)
		}
		return out, nil
	case <-ctx.Done():
		return out, ctx.Err()
	}
}

// The same synthetic Chinese audio is sent to the same Tencent legacy engine
// at three silence thresholds. Final event timing is compared with a known
// silent interval, but NO correctness assertions are made about ideal split
// boundaries or real conversational accuracy.
func TestTencentLiveVADComparison(t *testing.T) {
	source := livePCMFixture(t, "TENCENT_TEST_16K_PCM_FILE")
	pcm, firstEnd, secondStart, err := buildVADComparisonPCM(source)
	if err != nil {
		t.Fatal(err)
	}
	total := time.Duration(len(pcm)) * time.Second / (16000 * 2)
	t.Logf("VAD_COMPARE_FIXTURE first_speech_end_ms=%d second_speech_start_ms=%d total_audio_ms=%d injected_pause_ms=1200",
		firstEnd.Milliseconds(), secondStart.Milliseconds(), total.Milliseconds())
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	for _, threshold := range []time.Duration{700 * time.Millisecond, 1000 * time.Millisecond, 1500 * time.Millisecond} {
		result, err := runTencentVADComparison(ctx, pcm, threshold)
		if err != nil {
			t.Fatalf("VAD %s comparison stream: %v", threshold, err)
		}
		beforeSecond := 0
		for _, at := range result.finalOffsets {
			if at < secondStart {
				beforeSecond++
			}
		}
		firstRelative := result.finalOffsets[0] - firstEnd
		// These measurements are diagnostic: synthetic speech has internal
		// pauses, and server speech segmentation is not aligned to a transcript.
		t.Logf("VAD_COMPARE threshold_ms=%d first_final_relative_to_fixture_end_ms=%d finals_before_second_speech=%d finals_total=%d partials=%d stream_wall_ms=%d",
			threshold.Milliseconds(), firstRelative.Milliseconds(), beforeSecond,
			len(result.finalOffsets), result.partials, result.wallElapsed.Milliseconds())
	}
}
