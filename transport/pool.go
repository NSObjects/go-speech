// Package transport defines optional network resource management settings.
package transport

import "time"

// PoolConfig enables per-client WebSocket reuse. Zero fields select defaults.
// A nil *PoolConfig disables pooling. No connections are opened until needed.
type PoolConfig struct {
	MaxIdle        int           // Default: min(4, MaxOpen).
	MaxOpen        int           // Default: 16; includes active and dialing connections.
	IdleTimeout    time.Duration // Provider-specific: Aliyun 30s, Seed-TTS 10s.
	MaxLifetime    time.Duration // Default: 10m; checked when a connection is idle or returned.
	AcquireTimeout time.Duration // Default: caller context; otherwise bounds acquisition and dialing.
}

// PoolCapabilities describes local WebSocket reuse support and configuration.
// The zero value means unsupported. Supported, Warmup, VoiceSpecific and
// the operation flags describe implementation support independent of Enabled.
// Enabled reports whether a pool is configured and has not been closed.
// This is a snapshot, not a guarantee of capacity, liveness or entitlement.
type PoolCapabilities struct {
	Supported     bool // Supports optional WebSocket pooling.
	Enabled       bool // Configured and not closed at the time of the query.
	Warmup        bool // Implements explicit TTS prewarming; requires Enabled.
	VoiceSpecific bool // Warmup requires the actual voice ID.
	Batch         bool // Recognize (ASR) or Synthesize (TTS) uses the pool.
	Streaming     bool // OpenStream (ASR) or SynthesizeStream (TTS) uses the pool.
	Duplex        bool // OpenDuplex (TTS) uses the pool.
}
