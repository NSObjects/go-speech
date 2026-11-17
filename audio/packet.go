package audio

// PacketDecoder converts one complete encoded codec packet at a time to
// signed PCM16LE bytes. Callers preserve packet boundaries, and reuse a
// decoder for successive packets in the same audio session to preserve codec
// history. It is different from DecoderStream, which reads/writes arbitrary
// fragments of a continuous file/container stream. PacketDecoder is not a
// network transport, RTP depacketizer, or VAD.
type PacketDecoder interface {
    Decode(packet []byte) ([]byte,error)
    OutputFormat() Format
    Close() error
}
