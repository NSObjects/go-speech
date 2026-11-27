package speech_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	speech "github.com/NSObjects/go-speech"
	"github.com/NSObjects/go-speech/asr"
	tencenttts "github.com/NSObjects/go-speech/tts/providers/tencent"
	"github.com/NSObjects/go-speech/transport"
	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func TestPoolCapabilitiesOffline(t *testing.T) {
	var dials atomic.Int32
	dialer := &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected network request")
	}}
	pool := &speech.PoolConfig{MaxOpen: 1}
	for _, tc := range []struct {
		name string
		config speech.ASRConfig
		want transport.PoolCapabilities
	}{
		{"aliyun-disabled", speech.AliyunASR{APIKey: "test", Dialer: dialer}, transport.PoolCapabilities{Supported: true, Batch: true, Streaming: true}},
		{"aliyun-enabled", speech.AliyunASR{APIKey: "test", Dialer: dialer, Pool: pool}, transport.PoolCapabilities{Supported: true, Enabled: true, Batch: true, Streaming: true}},
		{"volcengine", speech.VolcengineASR{APIKey: "test", Dialer: dialer}, transport.PoolCapabilities{}},
		{"tencent", speech.TencentASR{AppID: "123", SecretID: "id", SecretKey: "key", Dialer: dialer}, transport.PoolCapabilities{}},
	} {
		t.Run("asr/"+tc.name, func(t *testing.T) {
			c, err := speech.NewASR(tc.config)
			if err != nil {
				t.Fatal(err)
			}
			cp := c.(asr.CapabilityProvider)
			if got := cp.Capabilities().Pool; got != tc.want {
				t.Fatalf("pool = %+v, want %+v", got, tc.want)
			}
			if tc.want.Supported {
				if err := c.(io.Closer).Close(); err != nil {
					t.Fatal(err)
				}
				want := tc.want
				want.Enabled = false
				if got := cp.Capabilities().Pool; got != want {
					t.Fatalf("closed pool = %+v, want %+v", got, want)
				}
			}
		})
	}
	for _, tc := range []struct {
		name string
		config speech.TTSConfig
		want transport.PoolCapabilities
	}{
		{"aliyun-disabled", speech.AliyunTTS{APIKey: "test", Dialer: dialer}, transport.PoolCapabilities{Supported: true, Warmup: true, Batch: true, Streaming: true, Duplex: true}},
		{"aliyun-enabled", speech.AliyunTTS{APIKey: "test", Dialer: dialer, Pool: pool}, transport.PoolCapabilities{Supported: true, Enabled: true, Warmup: true, Batch: true, Streaming: true, Duplex: true}},
		{"volcengine-disabled", speech.VolcengineTTS{APIKey: "test", Dialer: dialer}, transport.PoolCapabilities{Supported: true, Warmup: true, VoiceSpecific: true, Duplex: true}},
		{"volcengine-enabled", speech.VolcengineTTS{APIKey: "test", Dialer: dialer, Pool: pool}, transport.PoolCapabilities{Supported: true, Enabled: true, Warmup: true, VoiceSpecific: true, Duplex: true}},
		{"tencent", speech.TencentTTS{AppID: "123", SecretID: "id", SecretKey: "key", Dialer: dialer}, transport.PoolCapabilities{}},
	} {
		t.Run("tts/"+tc.name, func(t *testing.T) {
			c, err := speech.NewDuplexTTS(tc.config)
			if err != nil {
				t.Fatal(err)
			}
			cp := c.(tts.CapabilityProvider)
			got := cp.Capabilities().Pool
			if got != tc.want {
				t.Fatalf("pool = %+v, want %+v", got, tc.want)
			}
			if _, ok := c.(tts.Prewarmer); ok != got.Warmup {
				t.Fatal("warmup capability disagrees with implemented interface")
			}
			if tc.want.Supported {
				// A returned capability value cannot mutate the client.
				got.Enabled = !got.Enabled
				if cp.Capabilities().Pool != tc.want {
					t.Fatal("capability snapshot mutated client state")
				}
				// Queries may run concurrently with shutdown.
				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range 100 {
						state := cp.Capabilities().Pool
						state.Enabled = tc.want.Enabled
						if state != tc.want {
							t.Error("support flags changed during shutdown")
						}
					}
				}()
				err := c.(io.Closer).Close()
				wg.Wait()
				if err != nil {
					t.Fatal(err)
				}
				want := tc.want
				want.Enabled = false
				if got := cp.Capabilities().Pool; got != want {
					t.Fatalf("closed pool = %+v, want %+v", got, want)
				}
			}
		})
	}
	c, err := tencenttts.NewHTTP(tencenttts.HTTPConfig{SecretID: "id", SecretKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Capabilities().Pool; got != (transport.PoolCapabilities{}) {
		t.Fatalf("Tencent HTTP advertises WebSocket pool: %+v", got)
	}
	if dials.Load() != 0 {
		t.Fatalf("capability queries initiated %d dials", dials.Load())
	}
}
