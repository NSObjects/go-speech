package volcengine

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/NSObjects/go-speech/speecherr"
)

const maxWirePayload = 8 << 20

const (
	kindFullResponse = 9
	kindAudioResponse = 11
	kindServerError = 15
	eventSessionFinished = 152
	eventTTSAudio = 352
)

type wireFrame struct {
	kind byte
	event uint32
	session string
	code uint32
	payload []byte
}

// encodeRequest sends a v1, JSON, uncompressed full-client request.
func encodeRequest(payload []byte) ([]byte, error) {
	if len(payload) > maxWirePayload {
		return nil, protocol("request exceeds frame size limit")
	}
	frame := make([]byte, 8+len(payload))
	frame[0] = 0x11
	frame[1] = 0x10
	frame[2] = 0x10
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame, nil
}

// parseResponse validates variable-length event/session framing before
// exposing audio. Volcengine audio messages contain raw audio after the
// protocol envelope; they are not standalone files per WebSocket message.
func parseResponse(data []byte) (wireFrame, error) {
	var result wireFrame
	if len(data) < 8 || data[0] != 0x11 || data[3] != 0 {
		return result, protocol("invalid V3 frame header")
	}
	result.kind = data[1] >> 4
	flags := data[1] & 0x0f
	compression := data[2] & 0x0f
	serial := data[2] >> 4
	if compression != 0 && compression != 1 {
		return result, protocol("unsupported compression")
	}
	if serial != 0 && serial != 1 {
		return result, protocol("unsupported serialization")
	}
	pos := 4
	switch result.kind {
	case kindFullResponse, kindAudioResponse:
		if flags != 4 || len(data)-pos < 8 {
			return result, protocol("missing response event or session")
		}
		result.event = binary.BigEndian.Uint32(data[pos : pos+4])
		pos += 4
		sessionSize := uint64(binary.BigEndian.Uint32(data[pos : pos+4]))
		pos += 4
		if sessionSize > 128 || sessionSize > uint64(len(data)-pos) {
			return result, protocol("invalid session ID size")
		}
		result.session = string(data[pos : pos+int(sessionSize)])
		pos += int(sessionSize)
	case kindServerError:
		if flags != 0 || len(data)-pos < 4 {
			return result, protocol("truncated server error code")
		}
		result.code = binary.BigEndian.Uint32(data[pos : pos+4])
		pos += 4
	default:
		return result, protocol("unsupported response message type")
	}
	if len(data)-pos < 4 {
		return result, protocol("missing response payload length")
	}
	size := uint64(binary.BigEndian.Uint32(data[pos : pos+4]))
	pos += 4
	if size > maxWirePayload || size != uint64(len(data)-pos) {
		return result, protocol("invalid response payload size")
	}
	payload := data[pos:]
	if compression == 1 {
		r, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return result, protocol("invalid gzip response")
		}
		decoded, err := io.ReadAll(io.LimitReader(r, maxWirePayload+1))
		_ = r.Close()
		if err != nil || len(decoded) > maxWirePayload {
			return result, protocol("invalid or oversized decompressed response")
		}
		payload = decoded
	}
	result.payload = payload
	return result, nil
}

// SessionFinished (event 152) confirms normal completion. The official
// unidirectional API may return usage metadata without status_code; a status
// field, when present, must still indicate success.
func (f wireFrame) sessionStatus() error {
	if len(bytes.TrimSpace(f.payload)) == 0 {
		return nil
	}
	// Usage-only completion messages are valid, but an explicit null or
	// malformed status_code must not be silently interpreted as success.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(f.payload, &fields); err != nil || fields == nil {
		return protocol("invalid SessionFinished metadata")
	}
	raw, exists := fields["status_code"]
	if !exists {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		// Some valid provider completion messages explicitly send null.
		return nil
	}
	var status int
	if err := json.Unmarshal(raw, &status); err != nil {
		return protocol("invalid SessionFinished status_code")
	}
	if status != 20000000 {
		var info struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(f.payload, &info)
		if status < 0 {
			return protocol("negative SessionFinished status_code")
		}
		return remoteError(uint32(status), info.Message)
	}
	return nil
}

func (f wireFrame) failure() error {
	message := string(f.payload)
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(f.payload, &body) == nil && body.Message != "" {
		message = body.Message
	}
	return remoteError(f.code, message)
}
func remoteError(remote uint32, message string) error {
	code := speecherr.CodeInternal
	retryable := false
	switch {
	case remote == 401 || remote == 403 || remote == 45000000:
		code = speecherr.CodeUnauthorized
	case remote == 429:
		code, retryable = speecherr.CodeRateLimited, true
	case remote >= 40000000 && remote < 50000000:
		code = speecherr.CodeInvalidArgument
	case remote >= 50000000 && remote < 60000000:
		code, retryable = speecherr.CodeUnavailable, true
	}
	return &speecherr.Error{
		Code: code, Provider: "volcengine", Operation: "tts",
		RemoteCode: strconv.FormatUint(uint64(remote), 10),
		Retryable: retryable,
		Err: fmt.Errorf("Volcengine TTS error %d: %s", remote, message),
	}
}

