# WebSocket connection management

## Supported transports

| Provider | ASR connection reuse | TTS connection reuse | Explicit TTS warmup |
| --- | --- | --- | --- |
| Aliyun DashScope | Yes | CosyVoice | `tts.Prewarmer`, voice ignored |
| Volcengine | No, bigmodel_async is one-shot | Seed-TTS 3.0 duplex only, keyed by voice URL | `tts.Prewarmer`, voice required |
| Tencent | No, signed voice_id per session | No, signed SessionId and synthesis parameters | OpenDuplex with the actual request |

HTTP synthesis is outside the WebSocket pool. Pooling is opt-in per
client. Use the same client across requests; each stream owns an exclusive
lease. Close all streams and close the pooled client at shutdown.

## Capability discovery

Query `asr.CapabilityProvider` or `tts.CapabilityProvider` via
`Capabilities().Pool`. This is an offline snapshot of local support.
`Supported` indicates that a client can be configured with a pool;
`Enabled` is true only for an initialized, nonclosed pool.
`Warmup` and `VoiceSpecific` describe implementation requirements
independently of Enabled. Use Enabled && Warmup for UI or optional
feature selection; the warmup helper itself requires no prior query.

| Provider path | Batch | Streaming | Duplex | Warmup | VoiceSpecific |
| --- | --- | --- | --- | --- | --- |
| Aliyun ASR | Yes | Yes | No | No | No |
| Aliyun TTS | Yes | Yes | Yes | Yes | No |
| Seed-TTS | No | No | Yes | Yes | Yes |
| Volcengine ASR / Tencent ASR / Tencent TTS (WS or HTTP) | No | No | No | No | No |

The operation flags describe **WebSocket pool applicability**, not whether
the operations themselves are supported. Seed-TTS complete-text requests
use HTTP, even if the duplex pool is enabled. Querying does not reserve a
connection, check its liveness, or verify remote entitlements. A concurrent
Close may still invalidate a previously observed enabled state.

## Limits and lifecycle

| PoolConfig | Aliyun | Volcengine Seed-TTS |
| --- | --- | --- |
| MaxOpen default | 16 | 16 |
| MaxIdle default | min(4, MaxOpen) | min(4, MaxOpen) |
| IdleTimeout default | 30s | 10s |
| IdleTimeout maximum | 45s | 15s |
| MaxLifetime default | 10m | 10m |
| AcquireTimeout default | Caller context | Caller context, also bounded by startup budget |

MaxOpen includes active, idle and dialing sockets. MaxIdle is global per
client, not per voice. MaxLifetime is checked on idle/return; it does not
forcibly interrupt active synthesis. A nil Pool disables reuse. Zero fields
select provider defaults.

Only a fully consumed successful terminal protocol event permits reuse.
Aliyun CosyVoice also supports reuse after provider-confirmed native
cancellation. Seed-TTS interruption does not return a socket to the pool.
Local close, cancellation, protocol error and network failure discard it.
A server can disconnect before the configured idle timeout; failures are
returned without automatic task replay.

Client Close releases idle sockets, prevents new pooled acquisitions and
lets active tasks finish or be canceled by their own contexts. A dial that
finishes after Close is rejected and its socket closed before its capacity
reservation is released.

Pool-specific failures are exposed through `speecherr.Error`: closed pool
is `CodeUnavailable` (not retryable), disabled warmup is `CodeUnsupported`,
invalid warmup count is `CodeInvalidArgument`, and a pool-local acquisition
deadline is `CodeTimeout`. Caller context cancellation/deadlines and
underlying causes remain observable. No failure triggers automatic retry.

## Warmup and pre-open

Call `tts.Warmup(ctx, client, voice, count)` with enabled pooling.
The helper accepts clients from either constructor, delegates to
`tts.Prewarmer`, and returns unsupported or provider errors unchanged
(no retries or silent fallback). Capability queries are optional for
ordinary synthesis and explicit warmup.
The count is a best-effort target, bounded by MaxIdle. Existing matching
idle sockets and in-flight warmups are counted. The call does not wait for
occupied capacity, evict idle sockets, start synthesis or retry failed
dials. Previously warmed sockets remain governed by the pool, not by the
warmup context. No automatic MinIdle, refill or heartbeat is scheduled.

Aliyun ignores voice. Seed-TTS requires the actual voice ID; output format
and synthesis settings are supplied when the task begins.

For Tencent TTS, use `OpenDuplex(ctx, request)` before text is available,
then `SendText`, `CloseSend` and drain `Recv` to EOF. The context must
remain valid for the whole stream. Close a pre-opened stream if it will not
be used. There is no detached prepared-session cache. Do not assume an
arbitrary idle duration is supported. For nonpooled ASR, open a stream
when audio is about to arrive, respecting the provider's audio deadlines.

## Live protocol evidence

- Aliyun ASR and CosyVoice: [reuse validation](https://github.com/NSObjects/go-speech/pull/117).
- Seed-TTS 3.0: [reuse and idle policy](https://github.com/NSObjects/go-speech/pull/131).
  A post-CLOSED socket was reusable after 15s but closed after 30s in a
  live experiment. The local idle policy uses a margin, not a guaranteed
  service timeout. Post-interruption reuse was not established.
- Aliyun and Seed-TTS: [fresh socket warmup validation](https://github.com/NSObjects/go-speech/pull/132).
  A later audit verified fresh idle waits of 25s for Aliyun and 8s for
  Seed-TTS before first synthesis, with no additional TCP dial. These
  observations do not guarantee the server's maximum idle lifetime.
- Volcengine bigmodel_async ASR: [post-final experiment](https://github.com/NSObjects/go-speech/actions/runs/45964803903/job/140390836694).
  The first recognition completed and the server closed the socket with
  code 1000 and reason "finish last sequence" on attempted second init.
- Tencent TTS: [pre-open experiment](https://github.com/NSObjects/go-speech/actions/runs/45964803807/job/140390836509).
  Synthesis succeeded after a five-second wait following READY. This is a
  verified example, not a guarantee of the maximum permitted idle duration.

Official Tencent protocols:
[ASR V2](https://cloud.tencent.com/document/product/1093/48982),
[TTS WSv2](https://cloud.tencent.com/document/api/1073/108595).

See the provider-specific [Aliyun pool settings](aliyun-connection-pool.md)
and [Seed-TTS pool settings](../tts/providers/volcengine/README.md#optional-duplex-connection-reuse).
