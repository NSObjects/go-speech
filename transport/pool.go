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
