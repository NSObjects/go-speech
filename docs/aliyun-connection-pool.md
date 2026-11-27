# Aliyun WebSocket connection pooling

ASR and TTS clients support optional, per-client connection reuse:

```go
client, err := speech.NewTTS(speech.AliyunTTS{
    APIKey: apiKey,
    Pool: &speech.PoolConfig{
        MaxOpen: 16,
        MaxIdle: 4,
        IdleTimeout: 30 * time.Second,
    },
})
if err != nil {
    return err
}
defer client.(io.Closer).Close()
```

The same setting is available on `speech.AliyunASR` and the direct provider
constructors. Direct provider clients expose `Close()`; shared interfaces
remain unchanged. Keep the client alive across calls. A nil Pool preserves
one connection per task. Each task has a new task ID and stream, and exclusively
owns its socket until a matching task-finished event has been consumed.
Early Close, context cancellation, network failure and task-failed discard
the connection. Native TTS cancellation may reuse a connection after the
terminal acknowledgement. Do not use a stream after Close.

Zero configuration values use 16 open connections, up to four idle, a 30-second
idle timeout and ten-minute maximum lifetime. The idle timeout must be at most
45 seconds. Acquisition waits for capacity using the caller's context
(or the optional AcquireTimeout); no automatic task replay or idle refill.
A remote disconnect can still occur before the idle timeout and will be
reported to the caller. Close releases idle sockets and rejects new pooled
acquisitions; active tasks finish or are canceled through their own contexts.
