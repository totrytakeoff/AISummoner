// Package clientipc implements the private same-UID control protocol between
// the Remote daemon and its local desktop UI/CLI.
package clientipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/aisummoner/aisummoner/internal/remoteclient"
)

const (
	Version       = 1
	MaxFrameBytes = 64 * 1024
	MaxHandlers   = 8

	MethodStatusGet      = "status.get"
	MethodEventsList     = "events.list"
	MethodDaemonPause    = "daemon.pause"
	MethodDaemonResume   = "daemon.resume"
	MethodPairingRefresh = "pairing.refresh"
)

type Request struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Response struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type EventsListParams struct {
	AfterSequence uint64 `json:"after_sequence"`
	Limit         int    `json:"limit"`
}

type EventsListResult struct {
	Events       []remoteclient.Event `json:"events"`
	NextSequence uint64               `json:"next_sequence"`
}

type PairingRefreshResult struct {
	ClosesActiveSessions bool `json:"closes_active_sessions"`
}

type EmptyParams struct{}

type RemoteError struct {
	Code    string
	Message string
}

func (err *RemoteError) Error() string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", err.Code, err.Message)
}

func knownMethod(method string) bool {
	switch method {
	case MethodStatusGet, MethodEventsList, MethodDaemonPause, MethodDaemonResume, MethodPairingRefresh:
		return true
	default:
		return false
	}
}

func validRequestID(value string) bool {
	if len(value) < 5 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func decodeStrict(contents []byte, destination any) error {
	if err := rejectDuplicateObjectKeys(contents); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func rejectDuplicateObjectKeys(contents []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()
	return scanJSONValue(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if _, exists := keys[key]; exists {
				return errors.New("duplicate JSON object key")
			}
			keys[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}
