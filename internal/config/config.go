// Package config loads and validates AISummoner server configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultListenAddr       = "127.0.0.1:8080"
	defaultDataDir          = "data"
	defaultAgentAdapter     = "fake"
	defaultOpenCodeURL      = "http://127.0.0.1:4096"
	defaultDeepSeekURL      = "https://api.deepseek.com"
	defaultBridgeListenAddr = "127.0.0.1:4097"
	defaultBridgeURL        = "http://127.0.0.1:4097/internal/opencode/remote-exec"
	defaultWorkspaceDir     = "agent-workspaces"
	minimumSecretSize       = 32

	AgentAdapterFake     = "fake"
	AgentAdapterOpenCode = "opencode"
	AgentAdapterDeepSeek = "deepseek"
)

// Config is immutable after startup. Secret fields must never be logged.
type Config struct {
	BaseURL               *url.URL
	ListenAddr            string
	DataDir               string
	DatabasePath          string
	AdminPassword         string
	SessionSecret         []byte
	PairingSecret         []byte
	DevMode               bool
	CookieSecure          bool
	AllowedOrigin         string
	AgentAdapter          string
	AgentWorkspaceRoot    string
	OpenCodeURL           string
	OpenCodeUsername      string
	OpenCodePassword      string
	OpenCodeModel         string
	OpenCodeBridgeURL     string
	AgentBridgeListenAddr string
	AgentBridgeSecret     []byte
	DeepSeekURL           string
	DeepSeekAPIKey        string
	DeepSeekModel         string
	TrustedProxyIPs       []netip.Addr
}

type lookupEnv func(string) (string, bool)

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

func load(lookup lookupEnv) (Config, error) {
	devMode, err := envBool(lookup, "AISUMMONER_DEV_MODE", false)
	if err != nil {
		return Config{}, err
	}
	baseURLValue := env(lookup, "AISUMMONER_BASE_URL", "")
	if baseURLValue == "" {
		return Config{}, errors.New("AISUMMONER_BASE_URL is required")
	}
	baseURL, err := parseBaseURL(baseURLValue, devMode)
	if err != nil {
		return Config{}, err
	}
	listenAddr := env(lookup, "AISUMMONER_LISTEN_ADDR", defaultListenAddr)
	// Development HTTP disables the Secure cookie attribute, so it must never
	// be possible to expose that listener beyond the local machine. Production
	// HTTPS may bind broadly behind the deployment reverse proxy.
	if err := validateListenAddress(listenAddr, baseURL.Scheme == "http"); err != nil {
		return Config{}, fmt.Errorf("AISUMMONER_LISTEN_ADDR: %w", err)
	}
	dataDir, err := absoluteDirectory(env(lookup, "AISUMMONER_DATA_DIR", defaultDataDir), "AISUMMONER_DATA_DIR")
	if err != nil {
		return Config{}, err
	}

	sessionSecret, err := requiredSecret(lookup, "AISUMMONER_SESSION_SECRET")
	if err != nil {
		return Config{}, err
	}
	pairingSecret, err := requiredSecret(lookup, "AISUMMONER_PAIRING_SECRET")
	if err != nil {
		return Config{}, err
	}
	trustedProxyIPs, err := parseTrustedProxyIPs(env(lookup, "AISUMMONER_TRUSTED_PROXY_IPS", ""))
	if err != nil {
		return Config{}, err
	}

	configuration := Config{
		BaseURL:         baseURL,
		ListenAddr:      listenAddr,
		DataDir:         dataDir,
		DatabasePath:    filepath.Join(dataDir, "aisummoner.db"),
		AdminPassword:   env(lookup, "AISUMMONER_ADMIN_PASSWORD", ""),
		SessionSecret:   sessionSecret,
		PairingSecret:   pairingSecret,
		DevMode:         devMode,
		CookieSecure:    baseURL.Scheme == "https",
		AllowedOrigin:   baseURL.Scheme + "://" + baseURL.Host,
		AgentAdapter:    env(lookup, "AISUMMONER_AGENT_ADAPTER", defaultAgentAdapter),
		TrustedProxyIPs: trustedProxyIPs,
	}

	switch configuration.AgentAdapter {
	case AgentAdapterFake:
		// Fake mode deliberately does not read or validate any real-provider
		// credentials, endpoints, workspace paths, or bridge settings.
		return configuration, nil
	case AgentAdapterOpenCode:
		return loadOpenCode(configuration, lookup)
	case AgentAdapterDeepSeek:
		return loadDeepSeek(configuration, lookup)
	default:
		return Config{}, errors.New("AISUMMONER_AGENT_ADAPTER must be fake, opencode, or deepseek")
	}
}

func loadDeepSeek(configuration Config, lookup lookupEnv) (Config, error) {
	providerURL, err := parseDeepSeekOrigin(env(lookup, "AISUMMONER_DEEPSEEK_URL", defaultDeepSeekURL))
	if err != nil {
		return Config{}, err
	}
	apiKey := env(lookup, "AISUMMONER_DEEPSEEK_API_KEY", "")
	if !validVisibleASCII(apiKey, 4096) {
		return Config{}, errors.New("AISUMMONER_DEEPSEEK_API_KEY is required in deepseek mode")
	}
	model := env(lookup, "AISUMMONER_DEEPSEEK_MODEL", "")
	if !validVisibleASCII(model, 256) {
		return Config{}, errors.New("AISUMMONER_DEEPSEEK_MODEL is required in deepseek mode")
	}
	configuration.DeepSeekURL = providerURL.String()
	configuration.DeepSeekAPIKey = apiKey
	configuration.DeepSeekModel = model
	return configuration, nil
}

func validVisibleASCII(value string, maximumBytes int) bool {
	if value == "" || len(value) > maximumBytes {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func parseDeepSeekOrigin(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("AISUMMONER_DEEPSEEK_URL must be an HTTPS origin")
	}
	parsed.Path = ""
	return parsed, nil
}

func loadOpenCode(configuration Config, lookup lookupEnv) (Config, error) {
	openCodeURL, err := parseLoopbackOrigin(
		env(lookup, "AISUMMONER_OPENCODE_URL", defaultOpenCodeURL),
		"AISUMMONER_OPENCODE_URL",
	)
	if err != nil {
		return Config{}, err
	}
	username := env(lookup, "AISUMMONER_OPENCODE_USERNAME", "")
	password := env(lookup, "AISUMMONER_OPENCODE_PASSWORD", "")
	model := env(lookup, "AISUMMONER_OPENCODE_MODEL", "")
	if username == "" {
		return Config{}, errors.New("AISUMMONER_OPENCODE_USERNAME is required in opencode mode")
	}
	if password == "" {
		return Config{}, errors.New("AISUMMONER_OPENCODE_PASSWORD is required in opencode mode")
	}
	if strings.TrimSpace(model) == "" {
		return Config{}, errors.New("AISUMMONER_OPENCODE_MODEL is required in opencode mode")
	}

	workspaceRoot, err := absoluteDirectory(
		env(lookup, "AISUMMONER_AGENT_WORKSPACE_ROOT", filepath.Join(configuration.DataDir, defaultWorkspaceDir)),
		"AISUMMONER_AGENT_WORKSPACE_ROOT",
	)
	if err != nil {
		return Config{}, err
	}
	bridgeListenAddr := env(lookup, "AISUMMONER_AGENT_BRIDGE_LISTEN_ADDR", defaultBridgeListenAddr)
	if err := validateListenAddress(bridgeListenAddr, true); err != nil {
		return Config{}, fmt.Errorf("AISUMMONER_AGENT_BRIDGE_LISTEN_ADDR: %w", err)
	}
	bridgeURL, err := parseBridgeURL(env(lookup, "AISUMMONER_OPENCODE_BRIDGE_URL", defaultBridgeURL))
	if err != nil {
		return Config{}, err
	}
	if bridgeURL.Host != bridgeListenAddr {
		return Config{}, errors.New("AISUMMONER_OPENCODE_BRIDGE_URL authority must match AISUMMONER_AGENT_BRIDGE_LISTEN_ADDR")
	}
	bridgeSecret, err := requiredSecret(lookup, "AISUMMONER_AGENT_BRIDGE_SECRET")
	if err != nil {
		return Config{}, err
	}

	configuration.AgentWorkspaceRoot = workspaceRoot
	configuration.OpenCodeURL = openCodeURL.String()
	configuration.OpenCodeUsername = username
	configuration.OpenCodePassword = password
	configuration.OpenCodeModel = model
	configuration.OpenCodeBridgeURL = bridgeURL.String()
	configuration.AgentBridgeListenAddr = bridgeListenAddr
	configuration.AgentBridgeSecret = bridgeSecret
	return configuration, nil
}

// PreparePrivateDirectories creates the Server-owned directories with private
// permissions. Exact-path symlinks are rejected before chmod so startup cannot
// silently change permissions on an unrelated target.
func (configuration Config) PreparePrivateDirectories() error {
	directories := []struct {
		path string
		name string
	}{{configuration.DataDir, "AISUMMONER_DATA_DIR"}}
	if configuration.AgentAdapter == AgentAdapterOpenCode {
		directories = append(directories, struct {
			path string
			name string
		}{configuration.AgentWorkspaceRoot, "AISUMMONER_AGENT_WORKSPACE_ROOT"})
	}
	for _, directory := range directories {
		if err := preparePrivateDirectory(directory.path, directory.name); err != nil {
			return err
		}
	}
	return nil
}

func preparePrivateDirectory(path, name string) error {
	if path == "" {
		return fmt.Errorf("%s is required", name)
	}
	if err := rejectSymlinkComponents(path, name); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%s must name a directory, not a symlink or file", name)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %s: %w", name, err)
	} else if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	if err := rejectSymlinkComponents(path, name); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure %s: %w", name, err)
	}
	if info, err := os.Lstat(path); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s is not a private real directory", name)
	}
	return nil
}

func rejectSymlinkComponents(value, name string) error {
	for current := filepath.Clean(value); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s path contains a symlink", name)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect %s path: %w", name, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func parseBaseURL(value string, devMode bool) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, errors.New("invalid AISUMMONER_BASE_URL")
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawPath != "" {
		return nil, errors.New("AISUMMONER_BASE_URL must be an absolute URL without user info")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("AISUMMONER_BASE_URL must not contain a path, query, or fragment")
	}
	if parsed.Scheme != "https" {
		if parsed.Scheme != "http" || !devMode || !isLoopbackHost(parsed.Hostname()) {
			return nil, errors.New("AISUMMONER_BASE_URL must use https (loopback http requires AISUMMONER_DEV_MODE=1)")
		}
	}
	parsed.Path = ""
	return parsed, nil
}

func parseLoopbackOrigin(value, name string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.Opaque != "" || parsed.RawPath != "" || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("%s must be a loopback HTTP(S) origin", name)
	}
	if !isLiteralLoopback(parsed.Hostname()) {
		return nil, fmt.Errorf("%s must use a literal loopback address", name)
	}
	if _, err := normalizedPort(parsed); err != nil {
		return nil, fmt.Errorf("%s must contain a valid port", name)
	}
	parsed.Path = ""
	return parsed, nil
}

func parseBridgeURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "/internal/opencode/remote-exec" {
		return nil, errors.New("AISUMMONER_OPENCODE_BRIDGE_URL must be the exact loopback HTTP callback URL")
	}
	if !isLiteralLoopback(parsed.Hostname()) {
		return nil, errors.New("AISUMMONER_OPENCODE_BRIDGE_URL must use a literal loopback address")
	}
	if _, err := normalizedPort(parsed); err != nil {
		return nil, errors.New("AISUMMONER_OPENCODE_BRIDGE_URL must contain a valid port")
	}
	return parsed, nil
}

func normalizedPort(parsed *url.URL) (int, error) {
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "http" {
			return 80, nil
		}
		if parsed.Scheme == "https" {
			return 443, nil
		}
		return 0, errors.New("unsupported scheme")
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return 0, errors.New("invalid port")
	}
	return value, nil
}

func validateListenAddress(value string, loopbackOnly bool) error {
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return errors.New("must be a host:port address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("must contain a port between 1 and 65535")
	}
	if loopbackOnly && !isLiteralLoopback(host) {
		return errors.New("must use a literal loopback address")
	}
	return nil
}

func absoluteDirectory(value, name string) (string, error) {
	if value == "" || strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("%s must be a non-empty directory path", name)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	absolute = filepath.Clean(absolute)
	if absolute == string(filepath.Separator) {
		return "", fmt.Errorf("%s must not be the filesystem root", name)
	}
	return absolute, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	return isLiteralLoopback(host)
}

func isLiteralLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func requiredSecret(lookup lookupEnv, name string) ([]byte, error) {
	value := env(lookup, name, "")
	if len(value) < minimumSecretSize {
		return nil, fmt.Errorf("%s must contain at least %d bytes", name, minimumSecretSize)
	}
	return []byte(value), nil
}

func parseTrustedProxyIPs(value string) ([]netip.Addr, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	addresses := make([]netip.Addr, 0, len(parts))
	seen := make(map[netip.Addr]struct{}, len(parts))
	for _, part := range parts {
		if part == "" || part != strings.TrimSpace(part) {
			return nil, errors.New("AISUMMONER_TRUSTED_PROXY_IPS must contain unique exact literal unicast addresses")
		}
		address, err := netip.ParseAddr(part)
		if err != nil || address.Zone() != "" {
			return nil, errors.New("AISUMMONER_TRUSTED_PROXY_IPS must contain unique exact literal unicast addresses")
		}
		address = address.Unmap()
		if !validTrustedProxyIP(address) {
			return nil, errors.New("AISUMMONER_TRUSTED_PROXY_IPS must contain unique exact literal unicast addresses")
		}
		if _, duplicate := seen[address]; duplicate {
			return nil, errors.New("AISUMMONER_TRUSTED_PROXY_IPS must contain unique exact literal unicast addresses")
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address)
	}
	return addresses, nil
}

func validTrustedProxyIP(address netip.Addr) bool {
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	return !address.Is4() || address != netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

func env(lookup lookupEnv, name, fallback string) string {
	if value, ok := lookup(name); ok {
		return value
	}
	return fallback
}

func envBool(lookup lookupEnv, name string, fallback bool) (bool, error) {
	value, ok := lookup(name)
	if !ok || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}
