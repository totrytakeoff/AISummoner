//go:build windows

// Command windows-contract-probe provides a Windows-native interoperability
// seam for the bounded porting spike. It is not the production Remote daemon.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/aisummoner/aisummoner/internal/windowsprobe"
)

const maximumFrameBytes = 64 * 1024

type request struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Result  any    `json:"result"`
}

func main() {
	mode := flag.String("mode", "pipe-server", "pipe-server or facts")
	pipeName := flag.String("pipe", windowsprobe.DefaultQtPipeName, "Qt QLocalSocket server name")
	readyFile := flag.String("ready-file", "", "write this marker after the listener is ready")
	flag.Parse()

	var err error
	switch *mode {
	case "facts":
		err = printFacts()
	case "pipe-server":
		err = servePipe(*pipeName, *readyFile)
	default:
		err = fmt.Errorf("unknown mode %q", *mode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func printFacts() error {
	facts, err := windowsprobe.CurrentTokenFacts()
	if err != nil {
		return err
	}
	directory, err := windowsprobe.LocalDataDirectory()
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		windowsprobe.TokenFacts
		LocalDataDirectory string `json:"local_data_directory"`
	}{TokenFacts: facts, LocalDataDirectory: directory})
}

func servePipe(name, readyFile string) error {
	listener, err := windowsprobe.ListenVerifiedPipe(name)
	if err != nil {
		return err
	}
	defer listener.Close()
	if readyFile != "" {
		if err := os.WriteFile(readyFile, []byte("ready\n"), 0o600); err != nil {
			return fmt.Errorf("write ready marker: %w", err)
		}
	}
	fmt.Printf("READY %s\n", name)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	semaphore := make(chan struct{}, 8)
	var handlers sync.WaitGroup
	defer handlers.Wait()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		select {
		case semaphore <- struct{}{}:
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer func() { <-semaphore }()
				handleConnection(connection)
			}()
		default:
			_ = connection.Close()
		}
	}
}

func handleConnection(connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	frame, err := bufio.NewReaderSize(connection, maximumFrameBytes+1).ReadBytes('\n')
	if err != nil || len(frame) < 2 || len(frame) > maximumFrameBytes {
		return
	}
	var incoming request
	if err := json.Unmarshal(frame[:len(frame)-1], &incoming); err != nil || incoming.Version != 1 {
		return
	}
	var result any
	switch incoming.Method {
	case "status.get":
		result = map[string]any{
			"device_id": "dev_windows_contract", "device_name": "Windows Action",
			"client_version": "0.1.0", "server_origin": "https://control.example",
			"phase": "online", "active_sessions": 0,
			"updated_at": "2026-08-31T00:00:00Z",
		}
	case "events.list":
		result = map[string]any{
			"events": []map[string]any{{
				"sequence": 1, "at": "2026-08-31T00:00:01Z",
				"kind": "tunnel.online", "level": "info",
				"summary": "Windows Qt named-pipe interoperability verified",
			}},
			"next_sequence": 1,
		}
	default:
		result = map[string]any{}
	}
	fmt.Printf("REQUEST %s\n", incoming.Method)
	encoded, err := json.Marshal(response{Version: 1, ID: incoming.ID, OK: true, Result: result})
	if err != nil || len(encoded)+1 > maximumFrameBytes {
		return
	}
	_, _ = connection.Write(append(encoded, '\n'))
}
