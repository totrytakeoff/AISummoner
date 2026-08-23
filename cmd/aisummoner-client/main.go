package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aisummoner/aisummoner/internal/clientipc"
	"github.com/aisummoner/aisummoner/internal/remoteclient"
)

const clientVersion = "0.1.0"

type launchOptions struct {
	serverURL            string
	dataDirectory        string
	deviceName           string
	development          bool
	allowRootDevelopment bool
	socketPath           string
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("client stopped", "error", err)
		os.Exit(1)
	}
}

func run(arguments []string, logger *slog.Logger) error {
	if len(arguments) == 0 {
		return usageError()
	}
	switch arguments[0] {
	case "start":
		options, err := parseLaunchOptions("start", arguments[1:], false)
		if err != nil {
			return err
		}
		return runForeground(options, logger, os.Stdout)
	case "daemon":
		options, err := parseLaunchOptions("daemon", arguments[1:], true)
		if err != nil {
			return err
		}
		return runDaemon(options, logger)
	case "status", "pause", "resume", "refresh-pairing":
		dataDirectory, socketPath, err := parseControlOptions(arguments[0], arguments[1:])
		if err != nil {
			return err
		}
		if socketPath == "" {
			socketPath = clientipc.DefaultSocketPath(dataDirectory)
		}
		return runControl(arguments[0], socketPath, os.Stdout)
	default:
		return usageError()
	}
}

func runForeground(options launchOptions, logger *slog.Logger, pairingOutput io.Writer) error {
	controller, err := newController(options, logger)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("starting device client", "device_id", controller.DeviceID())
	runDone := make(chan error, 1)
	go func() { runDone <- controller.Run(ctx) }()
	for {
		select {
		case err := <-runDone:
			return err
		case offer := <-controller.PairingOffers():
			// Pairing codes are intentionally shown only to the interactive stdout
			// consumer and are never passed through structured logs. This write is
			// outside the Tunnel callback so output backpressure cannot hold cleanup.
			_, _ = fmt.Fprintf(pairingOutput, "Pairing code: %s (expires %s)\n", offer.Code, offer.ExpiresAt.UTC().Format(time.RFC3339))
		}
	}
}

func runDaemon(options launchOptions, logger *slog.Logger) error {
	controller, err := newController(options, logger)
	if err != nil {
		return err
	}
	server, err := clientipc.NewServer(clientipc.ServerOptions{
		SocketPath: options.socketPath, Controller: controller, Logger: logger,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- controller.Run(runContext) }()
	go func() { results <- server.Serve(runContext) }()
	logger.Info("starting device daemon", "device_id", controller.DeviceID())
	first := <-results
	cancel()
	second := <-results
	return errors.Join(first, second)
}

func newController(options launchOptions, logger *slog.Logger) (*remoteclient.Controller, error) {
	return remoteclient.New(remoteclient.Options{
		ServerURL: options.serverURL, DataDirectory: options.dataDirectory,
		DeviceName: options.deviceName, ClientVersion: clientVersion,
		Development: options.development, Logger: logger,
	})
}

func runControl(command, socketPath string, output io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	message := "ok"
	switch command {
	case "status":
		var snapshot remoteclient.Snapshot
		if err := clientipc.Call(ctx, socketPath, clientipc.MethodStatusGet, clientipc.EmptyParams{}, &snapshot); err != nil {
			return err
		}
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(snapshot)
	case "pause":
		if err := clientipc.Call(ctx, socketPath, clientipc.MethodDaemonPause, clientipc.EmptyParams{}, &struct{}{}); err != nil {
			return err
		}
	case "resume":
		if err := clientipc.Call(ctx, socketPath, clientipc.MethodDaemonResume, clientipc.EmptyParams{}, &struct{}{}); err != nil {
			return err
		}
	case "refresh-pairing":
		var result clientipc.PairingRefreshResult
		if err := clientipc.Call(ctx, socketPath, clientipc.MethodPairingRefresh, clientipc.EmptyParams{}, &result); err != nil {
			return err
		}
		if !result.ClosesActiveSessions {
			return errors.New("invalid pairing refresh response")
		}
		message = "ok; active control sessions were closed"
	default:
		return usageError()
	}
	_, err := fmt.Fprintln(output, message)
	return err
}

func parseLaunchOptions(command string, arguments []string, daemon bool) (launchOptions, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return launchOptions{}, fmt.Errorf("find user home: %w", err)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	serverURL := flags.String("server", "", "AISummoner Server base URL")
	dataDirectory := flags.String("data-dir", filepath.Join(home, ".local", "share", "aisummoner"), "private client data directory")
	deviceName := flags.String("name", "", "device display name (defaults to hostname)")
	development := flags.Bool("dev", false, "allow plaintext ws for local development")
	allowRootDevelopment := flags.Bool("allow-root-dev", false, "dangerously allow root only in development mode")
	socketPath := flags.String("socket", "", "private local daemon socket")
	if err := flags.Parse(arguments); err != nil {
		return launchOptions{}, errors.New("invalid client arguments")
	}
	if flags.NArg() != 0 || *serverURL == "" {
		return launchOptions{}, errors.New("--server is required and positional arguments are not accepted")
	}
	if !daemon && *socketPath != "" {
		return launchOptions{}, errors.New("--socket is available only in daemon mode")
	}
	if err := validateRootMode(os.Geteuid(), *development, *allowRootDevelopment); err != nil {
		return launchOptions{}, err
	}
	if *deviceName == "" {
		*deviceName, err = os.Hostname()
		if err != nil {
			return launchOptions{}, fmt.Errorf("read hostname: %w", err)
		}
	}
	if daemon && *socketPath == "" {
		*socketPath = clientipc.DefaultSocketPath(*dataDirectory)
	}
	if daemon && (!filepath.IsAbs(*dataDirectory) || !filepath.IsAbs(*socketPath) ||
		filepath.Clean(filepath.Dir(*socketPath)) != filepath.Clean(*dataDirectory)) {
		return launchOptions{}, errors.New("daemon data directory must be absolute and contain its socket")
	}
	return launchOptions{
		serverURL: *serverURL, dataDirectory: *dataDirectory, deviceName: *deviceName,
		development: *development, allowRootDevelopment: *allowRootDevelopment,
		socketPath: *socketPath,
	}, nil
}

func parseControlOptions(command string, arguments []string) (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("find user home: %w", err)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dataDirectory := flags.String("data-dir", filepath.Join(home, ".local", "share", "aisummoner"), "private client data directory")
	socketPath := flags.String("socket", "", "private local daemon socket")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return "", "", errors.New("invalid local control arguments")
	}
	if !filepath.IsAbs(*dataDirectory) || (*socketPath != "" &&
		(!filepath.IsAbs(*socketPath) || filepath.Clean(filepath.Dir(*socketPath)) != filepath.Clean(*dataDirectory))) {
		return "", "", errors.New("local control data directory must be absolute and contain its socket")
	}
	return *dataDirectory, *socketPath, nil
}

func validateRootMode(effectiveUID int, development, allowRootDevelopment bool) error {
	if effectiveUID == 0 && !(development && allowRootDevelopment) {
		return errors.New("refusing to run as root (development requires both --dev and --allow-root-dev)")
	}
	return nil
}

func usageError() error {
	return errors.New("usage: aisummoner-client <start|daemon|status|pause|resume|refresh-pairing> [options]")
}
