// Package protocol defines AISummoner's versioned tunnel control protocol.
package protocol

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	Version              = 1
	MaxControlFrameBytes = 64 * 1024
	MaxRequestIDBytes    = 128

	StreamControl = "control"
	StreamSSH     = "ssh"

	TypeClientHello         = "client.hello"
	TypeServerChallenge     = "server.challenge"
	TypeDeviceProof         = "device.proof"
	TypeServerAuthenticated = "server.authenticated"
	TypePairingOffered      = "pairing.offered"
	TypeDeviceHeartbeat     = "device.heartbeat"
	TypeHeartbeatAck        = "server.heartbeat_ack"
)

var (
	ErrFrameTooLarge    = errors.New("protocol frame is too large")
	ErrMalformedFrame   = errors.New("malformed protocol frame")
	ErrUnsupported      = errors.New("unsupported protocol version or message type")
	ErrInvalidRequestID = errors.New("invalid request id")
)

// StreamHeader is the first frame on every yamux stream.
type StreamHeader struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	RequestID string `json:"request_id"`
}

// Message is the envelope shared by every control message.
type Message struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"payload"`
}

type ClientHello struct {
	DeviceID        string `json:"device_id"`
	DevicePublicKey string `json:"device_public_key"`
	DeviceName      string `json:"device_name"`
	Platform        string `json:"platform"`
	Arch            string `json:"arch"`
	ClientVersion   string `json:"client_version"`
}

type ServerChallenge struct {
	Nonce string `json:"nonce"`
}

type DeviceProof struct {
	Signature string `json:"signature"`
}

type ServerAuthenticated struct {
	ConnectionID       string `json:"connection_id"`
	SSHClientPublicKey string `json:"ssh_client_public_key"`
	HeartbeatInterval  int64  `json:"heartbeat_interval_ms"`
}

type PairingOffered struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type DeviceHeartbeat struct {
	SentAt time.Time `json:"sent_at"`
}

type HeartbeatAck struct {
	ReceivedAt time.Time `json:"received_at"`
}

// Codec serializes independent JSON frames over a byte stream. Reads must have
// one caller; writes are safe for concurrent heartbeat and lifecycle senders.
type Codec struct {
	reader  *bufio.Reader
	writer  io.Writer
	writeMu sync.Mutex
}

func NewCodec(stream io.ReadWriter) *Codec {
	return &Codec{reader: bufio.NewReaderSize(stream, 4096), writer: stream}
}

func (c *Codec) WriteHeader(header StreamHeader) error {
	if err := ValidateRequestID(header.RequestID); err != nil {
		return err
	}
	if header.Version != Version || (header.Kind != StreamControl && header.Kind != StreamSSH) {
		return ErrUnsupported
	}
	return c.writeJSON(header)
}

func (c *Codec) ReadHeader() (StreamHeader, error) {
	var header StreamHeader
	if err := c.readJSON(&header); err != nil {
		return StreamHeader{}, err
	}
	if err := ValidateRequestID(header.RequestID); err != nil {
		return StreamHeader{}, err
	}
	if header.Version != Version || (header.Kind != StreamControl && header.Kind != StreamSSH) {
		return StreamHeader{}, ErrUnsupported
	}
	return header, nil
}

func (c *Codec) WriteMessage(messageType, requestID string, payload any) error {
	if !KnownMessageType(messageType) {
		return ErrUnsupported
	}
	if err := ValidateRequestID(requestID); err != nil {
		return err
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode protocol payload: %w", err)
	}
	return c.writeJSON(Message{
		Version: Version, Type: messageType, RequestID: requestID, Payload: encodedPayload,
	})
}

func (c *Codec) ReadMessage() (Message, error) {
	var message Message
	if err := c.readJSON(&message); err != nil {
		return Message{}, err
	}
	if message.Version != Version || !KnownMessageType(message.Type) {
		return Message{}, ErrUnsupported
	}
	if err := ValidateRequestID(message.RequestID); err != nil {
		return Message{}, err
	}
	if len(message.Payload) == 0 || string(message.Payload) == "null" {
		return Message{}, ErrMalformedFrame
	}
	return message, nil
}

// DecodePayload applies strict JSON field checking to a message payload.
func DecodePayload(message Message, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(message.Payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s payload: %w", message.Type, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode %s payload: %w", message.Type, err)
	}
	return nil
}

func KnownMessageType(messageType string) bool {
	switch messageType {
	case TypeClientHello, TypeServerChallenge, TypeDeviceProof, TypeServerAuthenticated,
		TypePairingOffered, TypeDeviceHeartbeat, TypeHeartbeatAck:
		return true
	default:
		return false
	}
}

func ValidateRequestID(requestID string) error {
	if len(requestID) < 5 || len(requestID) > MaxRequestIDBytes || !strings.HasPrefix(requestID, "req_") {
		return ErrInvalidRequestID
	}
	for _, character := range requestID[4:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return ErrInvalidRequestID
		}
	}
	return nil
}

func (c *Codec) writeJSON(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode protocol frame: %w", err)
	}
	if len(encoded) == 0 {
		return ErrMalformedFrame
	}
	if len(encoded) > MaxControlFrameBytes {
		return ErrFrameTooLarge
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(encoded)))
	if err := writeFull(c.writer, length[:]); err != nil {
		return fmt.Errorf("write protocol frame length: %w", err)
	}
	if err := writeFull(c.writer, encoded); err != nil {
		return fmt.Errorf("write protocol frame: %w", err)
	}
	return nil
}

func (c *Codec) readJSON(destination any) error {
	var length [4]byte
	if _, err := io.ReadFull(c.reader, length[:]); err != nil {
		return fmt.Errorf("read protocol frame length: %w", err)
	}
	size := binary.BigEndian.Uint32(length[:])
	if size == 0 {
		return ErrMalformedFrame
	}
	if size > MaxControlFrameBytes {
		return ErrFrameTooLarge
	}
	encoded := make([]byte, int(size))
	if _, err := io.ReadFull(c.reader, encoded); err != nil {
		return fmt.Errorf("read protocol frame: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode protocol frame: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode protocol frame: %w", err)
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return ErrMalformedFrame
		}
		return err
	}
	return nil
}

func writeFull(writer io.Writer, contents []byte) error {
	for len(contents) > 0 {
		written, err := writer.Write(contents)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		contents = contents[written:]
	}
	return nil
}
