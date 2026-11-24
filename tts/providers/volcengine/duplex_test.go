package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NSObjects/go-speech/tts"
	"github.com/gorilla/websocket"
)

func duplexResponse(event uint32, session string, kind byte, payload []byte) []byte {
	if event == duplexConnectionStarted && session == "" {
		session = "mock-connection"
	}
	size := 12 + len(payload)
	if session != "" {
		size += 4 + len(session)
	}
	frame := make([]byte, size)
	frame[0], frame[1], frame[2] = 0x11, kind<<4|4, 0x10
	if kind == kindAudioResponse {
		frame[2] = 0
	}
	binary.BigEndian.PutUint32(frame[4:8], event)
	pos := 8
	if session != "" {
		binary.BigEndian.PutUint32(frame[pos:pos+4], uint32(len(session)))
		pos += 4
		copy(frame[pos:], session)
		pos += len(session)
	}
	binary.BigEndian.PutUint32(frame[pos:pos+4], uint32(len(payload)))
	copy(frame[pos+4:], payload)
	return frame
}
func TestDuplexStreamingTextAndAudio(t *testing.T) {
	upgrader := websocket.Upgrader{}
	received := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/tts/bidirection" {
			http.Error(w, "wrong endpoint", 404)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, buf, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if binary.BigEndian.Uint32(buf[4:8]) != duplexStartConnection {
            return
        }
        if len(buf) != 14 || binary.BigEndian.Uint32(buf[8:12]) != 2 || !bytes.Equal(buf[12:], []byte("{}")) {
            t.Error("StartConnection must contain a valid JSON object")
            return
        }
		_ = conn.WriteMessage(websocket.BinaryMessage, duplexResponse(duplexConnectionStarted, "", kindFullResponse, nil))
		_, buf, err = conn.ReadMessage()
		if err != nil || binary.BigEndian.Uint32(buf[4:8]) != duplexStartSession {
			return
		}
		length := binary.BigEndian.Uint32(buf[8:12])
		session := string(buf[12 : 12+length])
        bodyAt := 12 + int(length)
        if len(buf) < bodyAt+4 {
            t.Error("truncated StartSession payload")
            return
        }
        payloadSize := int(binary.BigEndian.Uint32(buf[bodyAt:bodyAt+4]))
        if payloadSize != len(buf)-bodyAt-4 {
            t.Error("invalid StartSession payload length")
            return
        }
        var body struct { Namespace string `json:"namespace"` }
        if err := json.Unmarshal(buf[bodyAt+4:], &body); err != nil || body.Namespace != "BidirectionalTTS" {
            t.Errorf("StartSession missing namespace: %q, %v",body.Namespace,err)
            return
        }
		_ = conn.WriteMessage(websocket.BinaryMessage, duplexResponse(duplexSessionStarted, session, kindFullResponse, nil))
		count := 0
		for {
			_, buf, err = conn.ReadMessage()
			if err != nil {
				return
			}
			event := binary.BigEndian.Uint32(buf[4:8])
			switch event {
			case duplexTaskRequest:
				count++
				if count == 1 {
					_ = conn.WriteMessage(websocket.BinaryMessage, duplexResponse(eventTTSAudio, session, kindAudioResponse, []byte("audio")))
				}
			case duplexFinishSession:
				received <- count
				_ = conn.WriteMessage(websocket.BinaryMessage, duplexResponse(eventSessionFinished, session, kindFullResponse, nil))
				return
			}
		}
	}))
	defer srv.Close()
	client, err := New(Config{APIKey: "test", Endpoint: "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v3/tts/unidirectional/stream"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.OpenDuplex(ctx, tts.DuplexRequest{Voice: "test-voice"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.SendText(ctx, "你好"); err != nil {
		t.Fatal(err)
	}
	chunk, err := stream.Recv(ctx)
	if err != nil || string(chunk.Data) != "audio" {
		t.Fatalf("audio before half-close: %q %v", chunk.Data, err)
	}
	if err := stream.SendText(ctx, "世界"); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseSend(ctx); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(stream.SendText(ctx, "again"), tts.ErrInputClosed) {
		t.Fatal("expected ErrInputClosed")
	}
	if _, err := stream.Recv(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
	if count := <-received; count != 2 {
		t.Fatalf("got %d text messages", count)
	}
}
