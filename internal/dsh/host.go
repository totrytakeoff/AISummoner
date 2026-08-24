package dsh

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	EnvBridgeURL    = "AISUMMONER_DSH_BRIDGE_URL"
	EnvBridgeSecret = "AISUMMONER_AGENT_BRIDGE_SECRET"

	defaultStartupPollInterval  = 100 * time.Millisecond
	defaultHealthAttemptTimeout = 2 * time.Second
	defaultTerminationGrace     = 5 * time.Second
)

// HealthProbe is the narrow startup contract implemented by Adapter.
type HealthProbe interface {
	Health(context.Context) HealthResult
}

// HostOptions describes one private, pinned DSH Host process. Executable and
// CLI paths are absolute so startup never resolves code through PATH.
type HostOptions struct {
	NodePath     string
	CLIPath      string
	Home         string
	BaseURL      string
	BridgeURL    string
	BridgeSecret []byte
	Probe        HealthProbe

	StartupPollInterval  time.Duration
	HealthAttemptTimeout time.Duration
	TerminationGrace     time.Duration
}

// Host owns exactly one DSH child process and its Wait call.
type Host struct {
	command          *exec.Cmd
	waitDone         chan struct{}
	terminationGrace time.Duration

	closeOnce sync.Once
	closeDone chan struct{}
}

// StartHost installs the reviewed runtime overlay, starts DSH with an
// allowlisted environment, and waits for the private Host RPC to become ready.
func StartHost(ctx context.Context, options HostOptions) (*Host, error) {
	if ctx == nil {
		return nil, errors.New("DSH startup context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Probe == nil {
		return nil, errors.New("DSH health probe is required")
	}
	nodePath, err := validateRuntimeFile(options.NodePath, "DSH Node executable")
	if err != nil {
		return nil, err
	}
	cliPath, err := validateRuntimeFile(options.CLIPath, "DSH CLI entrypoint")
	if err != nil {
		return nil, err
	}
	baseURL, err := validateBaseURL(options.BaseURL)
	if err != nil {
		return nil, err
	}
	if baseURL.Port() == "" {
		return nil, errors.New("DSH base URL must contain an explicit port")
	}
	bridgeURL, err := validatePrivateBridgeURL(options.BridgeURL)
	if err != nil {
		return nil, err
	}
	if len(options.BridgeSecret) < 32 || strings.IndexByte(string(options.BridgeSecret), 0) >= 0 {
		return nil, errors.New("DSH bridge secret must contain at least 32 bytes")
	}
	runtimeFiles, err := PrepareRuntimeHome(options.Home)
	if err != nil {
		return nil, err
	}
	if options.StartupPollInterval <= 0 {
		options.StartupPollInterval = defaultStartupPollInterval
	}
	if options.HealthAttemptTimeout <= 0 {
		options.HealthAttemptTimeout = defaultHealthAttemptTimeout
	}
	if options.TerminationGrace <= 0 {
		options.TerminationGrace = defaultTerminationGrace
	}

	command := exec.Command(nodePath,
		cliPath,
		"--profile", "web",
		"--patch", runtimeFiles.PatchPath,
		"--host", baseURL.Hostname(),
		"--port", baseURL.Port(),
	)
	command.Dir = options.Home
	command.Stdin = nil
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.Env = []string{
		"HOME=" + options.Home,
		"DSH_HOME=" + options.Home,
		"DSH_TELEMETRY_DISABLED=1",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"NO_COLOR=1",
		"PATH=" + filepath.Dir(nodePath),
		"TZ=UTC",
		EnvBridgeURL + "=" + bridgeURL.String(),
		EnvBridgeSecret + "=" + string(options.BridgeSecret),
	}
	if err := command.Start(); err != nil {
		return nil, errors.New("start private DSH Host")
	}
	// The environment has been copied into the child. Do not retain the bridge
	// secret in the supervisor's command value for the rest of the process.
	command.Env = nil
	host := &Host{
		command: command, waitDone: make(chan struct{}), terminationGrace: options.TerminationGrace,
		closeDone: make(chan struct{}),
	}
	go func() {
		_ = command.Wait()
		close(host.waitDone)
	}()

	if err := host.awaitReady(ctx, options.Probe, options.StartupPollInterval, options.HealthAttemptTimeout); err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), options.TerminationGrace+time.Second)
		defer cancel()
		_ = host.Close(closeCtx)
		return nil, err
	}
	return host, nil
}

func (host *Host) awaitReady(ctx context.Context, probe HealthProbe, pollInterval, attemptTimeout time.Duration) error {
	for {
		select {
		case <-host.waitDone:
			return errors.New("private DSH Host exited during startup")
		default:
		}
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		health := probe.Health(attemptCtx)
		cancel()
		if health.Status == HealthAvailable {
			select {
			case <-host.waitDone:
				return errors.New("private DSH Host exited during startup")
			default:
				return nil
			}
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-host.waitDone:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return errors.New("private DSH Host exited during startup")
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return errors.New("private DSH Host did not become ready")
		case <-timer.C:
		}
	}
}

// Done closes only after the owned child has been reaped.
func (host *Host) Done() <-chan struct{} { return host.waitDone }

// Close is idempotent and joined. Cleanup continues when a caller's context
// expires; a grace timeout escalates only this exact child to Kill.
func (host *Host) Close(ctx context.Context) error {
	if host == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	host.closeOnce.Do(func() { go host.finishClose() })
	select {
	case <-host.closeDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (host *Host) finishClose() {
	defer close(host.closeDone)
	select {
	case <-host.waitDone:
		return
	default:
	}
	_ = host.command.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(host.terminationGrace)
	defer timer.Stop()
	select {
	case <-host.waitDone:
		return
	case <-timer.C:
		_ = host.command.Process.Kill()
		<-host.waitDone
	}
}

func validateRuntimeFile(value, name string) (string, error) {
	if !filepath.IsAbs(value) || filepath.Clean(value) == string(filepath.Separator) {
		return "", errors.New(name + " must be an absolute regular file")
	}
	value = filepath.Clean(value)
	if err := rejectSymlinkPath(value); err != nil {
		return "", errors.New(name + " path is not trusted")
	}
	info, err := os.Lstat(value)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New(name + " must be an absolute regular file")
	}
	return value, nil
}

func validatePrivateBridgeURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != CallbackPath {
		return nil, errors.New("DSH bridge URL must be the exact private callback URL")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() || parsed.Port() == "" {
		return nil, errors.New("DSH bridge URL must use a literal loopback host and explicit port")
	}
	return parsed, nil
}
