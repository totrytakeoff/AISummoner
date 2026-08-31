package clientipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aisummoner/aisummoner/internal/id"
)

const defaultCallTimeout = 5 * time.Second

func Call(ctx context.Context, endpoint, method string, params, result any) error {
	transport := currentTransport()
	if err := transport.ValidateEndpoint(endpoint); err != nil || !knownMethod(method) {
		return errors.New("invalid local daemon request")
	}
	requestID, err := id.New("req")
	if err != nil {
		return err
	}
	encodedParams, err := json.Marshal(params)
	if err != nil {
		return errors.New("invalid local daemon parameters")
	}
	request, err := json.Marshal(Request{Version: Version, ID: requestID, Method: method, Params: encodedParams})
	if err != nil || len(request)+1 > MaxFrameBytes {
		return errors.New("local daemon request is too large")
	}
	callContext := ctx
	var cancel context.CancelFunc
	if _, exists := ctx.Deadline(); !exists {
		callContext, cancel = context.WithTimeout(ctx, defaultCallTimeout)
		defer cancel()
	}
	connection, err := transport.Dial(callContext, endpoint)
	if err != nil {
		return fmt.Errorf("connect to local Remote service: %w", err)
	}
	defer connection.Close()
	if deadline, exists := callContext.Deadline(); exists {
		if err := connection.SetDeadline(deadline); err != nil {
			return err
		}
	}
	request = append(request, '\n')
	if _, err := connection.Write(request); err != nil {
		return fmt.Errorf("write local daemon request: %w", err)
	}
	contents, err := readClientFrame(connection)
	if err != nil {
		return err
	}
	var response Response
	if err := decodeStrict(contents, &response); err != nil || response.Version != Version || response.ID != requestID {
		return errors.New("invalid local daemon response")
	}
	if !response.OK {
		if response.Error == nil || response.Error.Code == "" || response.Error.Message == "" || len(response.Result) != 0 {
			return errors.New("invalid local daemon response")
		}
		return &RemoteError{Code: response.Error.Code, Message: response.Error.Message}
	}
	if response.Error != nil || len(response.Result) == 0 {
		return errors.New("invalid local daemon response")
	}
	if result == nil {
		return nil
	}
	if err := decodeStrict(response.Result, result); err != nil {
		return errors.New("invalid local daemon result")
	}
	return nil
}

func readClientFrame(reader io.Reader) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: MaxFrameBytes + 1}
	contents, err := bufio.NewReaderSize(limited, 4096).ReadBytes('\n')
	if err != nil || len(contents) < 2 || len(contents) > MaxFrameBytes || contents[len(contents)-1] != '\n' {
		return nil, errors.New("invalid local daemon response")
	}
	return contents[:len(contents)-1], nil
}
