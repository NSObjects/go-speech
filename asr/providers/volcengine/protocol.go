package volcengine

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/NSObjects/go-speech/speecherr"
)

const (
	messageFullRequest  = 1
	messageAudioRequest = 2
	messageFullResponse = 9
	messageServerError  = 15
	flagLast            = 2
	maxWirePayload      = 8 << 20
)

type wireMessage struct {
	kind    byte
	flags   byte
	seq     int32
	code    uint32
	payload []byte
}

func encodeFrame(kind, flags, serialization byte, payload []byte) ([]byte, error) {
	if len(payload) > maxWirePayload {
		return nil, errors.New("volcengine: audio or request frame exceeds size limit")
	}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(payload); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if compressed.Len() > maxWirePayload {
		return nil, errors.New("volcengine: compressed frame exceeds size limit")
	}
	frame := make([]byte, 8+compressed.Len())
	frame[0] = 0x11 // protocol v1, four-byte header
	frame[1] = kind<<4 | flags
	frame[2] = serialization<<4 | 1 // gzip
	binary.BigEndian.PutUint32(frame[4:8], uint32(compressed.Len()))
	copy(frame[8:], compressed.Bytes())
	return frame, nil
}

func parseFrame(data []byte) (wireMessage, error) {
	var message wireMessage
	if len(data) < 8 || data[0]>>4 != 1 || data[0]&0xf == 0 {
		return message, protocolError("short or invalid binary frame")
	}
	headerBytes := int(data[0]&0xf) * 4
	if headerBytes < 4 || headerBytes > len(data) {
		return message, protocolError("invalid protocol header length")
	}
	message.kind, message.flags = data[1]>>4, data[1]&0xf
	compression := data[2] & 0xf
	serialization := data[2] >> 4
	if data[3] != 0 || (compression != 0 && compression != 1) {
		return message, protocolError("unsupported protocol header or compression")
	}
	if message.kind != messageFullResponse && message.kind != messageServerError {
		return message, protocolError("unexpected server message type")
	}
	if serialization != 1 && serialization != 0 {
		return message, protocolError("unexpected response serialization")
	}
	pos := headerBytes
	if message.kind == messageServerError {
		if len(data)-pos < 8 {
			return message, protocolError("truncated server error")
		}
		message.code = binary.BigEndian.Uint32(data[pos : pos+4])
		pos += 4
	} else if message.flags&1 != 0 {
		if len(data)-pos < 8 {
			return message, protocolError("truncated server sequence")
		}
		message.seq = int32(binary.BigEndian.Uint32(data[pos : pos+4]))
		pos += 4
	}
	if len(data)-pos < 4 {
		return message, protocolError("missing payload size")
	}
	n := uint64(binary.BigEndian.Uint32(data[pos : pos+4]))
	pos += 4
	if n > maxWirePayload || uint64(len(data)-pos) != n {
		return message, protocolError("invalid or oversized payload length")
	}
	body := data[pos:]
	if compression == 1 {
		r, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return message, protocolWrap(err)
		}
		uncompressed, err := io.ReadAll(io.LimitReader(r, maxWirePayload+1))
		_ = r.Close()
		if err != nil {
			return message, protocolWrap(err)
		}
		if len(uncompressed) > maxWirePayload {
			return message, protocolError("inflated payload too large")
		}
		body = uncompressed
	}
	message.payload = body
	return message, nil
}

type utterance struct {
	Text     string `json:"text"`
	Definite bool   `json:"definite"`
	Start    int64  `json:"start_time"`
	End      int64  `json:"end_time"`
}

type recognition struct {
	Text       string      `json:"text"`
	Utterances []utterance `json:"utterances"`
}

type serverPayload struct {
	Code          int             `json:"code"`
	Message       string          `json:"message"`
	IsLastPackage bool            `json:"is_last_package"`
	Result        json.RawMessage `json:"result"`
	PayloadMsg    json.RawMessage `json:"payload_msg"`
}

// result decodes a server response. noSpeech distinguishes an explicit 1013
// result from an empty final response that may follow provisional speech.
func (m wireMessage) result() ([]recognition, bool, bool, error) {
	if m.kind == messageServerError {
		if m.code == 1013 { // provider: audio was silent; no recognized text
			return nil, true, true, nil
		}
		msg := string(m.payload)
		var body struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(m.payload, &body) == nil && body.Message != "" {
			msg = body.Message
		}
		code := strconv.FormatUint(uint64(m.code), 10)
		class := speecherr.CodeUnavailable
		switch m.code {
		case 401, 403:
			class = speecherr.CodeUnauthorized
		case 429:
			class = speecherr.CodeRateLimited
		}
		return nil, false, false, &speecherr.Error{
			Code: class, Provider: "volcengine", Operation: "receive",
			RemoteCode: code, Err: fmt.Errorf("remote error %s: %s", code, msg),
			Retryable: class == speecherr.CodeRateLimited || (class == speecherr.CodeUnavailable && m.code >= 50000000),
		}
	}
	var p serverPayload
	if err := json.Unmarshal(m.payload, &p); err != nil {
		return nil, false, false, protocolWrap(err)
	}
	// The top-level error is authoritative. Do not let an inner success
	// code or malformed payload_msg hide a provider authentication, quota,
	// or request failure.
	outerSuccess := p.Code == 0 || p.Code == 1000 || p.Code == 20000000
	if outerSuccess && len(p.PayloadMsg) != 0 && len(p.Result) == 0 {
		// Successful responses may wrap the result inside payload_msg.
		// Preserve the outer last-packet marker while unwrapping.
		var nested serverPayload
		if err := json.Unmarshal(p.PayloadMsg, &nested); err != nil {
			return nil, false, false, protocolWrap(err)
		}
		p.Result = nested.Result
		if nested.IsLastPackage {
			p.IsLastPackage = true
		}
		if nested.Code != 0 {
			p.Code = nested.Code
		}
		if nested.Message != "" {
			p.Message = nested.Message
		}
	}
	if p.Code != 0 && p.Code != 1000 && p.Code != 20000000 {
		code := speecherr.CodeUnavailable
		switch {
		case p.Code == 1013: // provider: audio was silent; no recognized text
			return nil, true, true, nil
		case p.Code == 401 || p.Code == 403:
			code = speecherr.CodeUnauthorized
		case p.Code == 429:
			code = speecherr.CodeRateLimited
		case p.Code >= 4000 && p.Code < 5000:
			code = speecherr.CodeInvalidArgument
		}
		return nil, false, false, &speecherr.Error{
			Code: code, Provider: "volcengine", Operation: "receive",
			RemoteCode: strconv.Itoa(p.Code),
			Retryable:  code == speecherr.CodeUnavailable || code == speecherr.CodeRateLimited,
			Err:        fmt.Errorf("remote response %d: %s", p.Code, p.Message),
		}
	}
	last := p.IsLastPackage || m.flags&flagLast != 0
	if len(p.Result) == 0 || bytes.Equal(bytes.TrimSpace(p.Result), []byte("null")) {
		return nil, last, false, nil
	}
	var single recognition
	if err := json.Unmarshal(p.Result, &single); err == nil {
		return []recognition{single}, last, false, nil
	}
	var batch []recognition
	if err := json.Unmarshal(p.Result, &batch); err != nil {
		return nil, false, false, protocolWrap(err)
	}
	return batch, last, false, nil
}

func protocolError(msg string) error {
	return &speecherr.Error{Code: speecherr.CodeProtocol, Provider: "volcengine", Operation: "decode", Err: errors.New(msg)}
}

func protocolWrap(err error) error {
	return &speecherr.Error{Code: speecherr.CodeProtocol, Provider: "volcengine", Operation: "decode", Err: err}
}
