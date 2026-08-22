package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCodecRoundTrip(t *testing.T) {
	var stream bytes.Buffer
	writer := NewCodec(&stream)
	want := DeviceHeartbeat{SentAt: time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)}
	if err := writer.WriteMessage(TypeDeviceHeartbeat, "req_abc123", want); err != nil {
		t.Fatal(err)
	}
	message, err := NewCodec(&stream).ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var got DeviceHeartbeat
	if err := DecodePayload(message, &got); err != nil {
		t.Fatal(err)
	}
	if !got.SentAt.Equal(want.SentAt) {
		t.Fatalf("sent_at = %v, want %v", got.SentAt, want.SentAt)
	}
}

func TestCodecRejectsOversizedDeclaredFrame(t *testing.T) {
	var stream bytes.Buffer
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], MaxControlFrameBytes+1)
	stream.Write(length[:])
	_, err := NewCodec(&stream).ReadMessage()
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("error = %v, want ErrFrameTooLarge", err)
	}
}

func TestCodecRejectsUnknownVersionTypeAndFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"version", `{"version":2,"type":"client.hello","request_id":"req_abc","payload":{}}`},
		{"type", `{"version":1,"type":"surprise","request_id":"req_abc","payload":{}}`},
		{"field", `{"version":1,"type":"client.hello","request_id":"req_abc","payload":{},"extra":true}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var framed bytes.Buffer
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(test.body)))
			framed.Write(length[:])
			framed.WriteString(test.body)
			if _, err := NewCodec(&framed).ReadMessage(); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestDecodePayloadRejectsUnknownField(t *testing.T) {
	message := Message{Type: TypeDeviceProof, Payload: []byte(`{"signature":"x","extra":true}`)}
	var payload DeviceProof
	if err := DecodePayload(message, &payload); err == nil {
		t.Fatal("expected unknown payload field rejection")
	}
}

func TestWriteRejectsOversizedPayload(t *testing.T) {
	var stream bytes.Buffer
	err := NewCodec(&stream).WriteMessage(TypeDeviceProof, "req_abc", DeviceProof{Signature: strings.Repeat("x", MaxControlFrameBytes)})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("error = %v, want ErrFrameTooLarge", err)
	}
}
