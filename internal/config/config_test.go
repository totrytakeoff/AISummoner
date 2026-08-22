package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDevelopmentFakeConfig(t *testing.T) {
	values := baseValues(t)
	values["AISUMMONER_OPENCODE_URL"] = "https://public.invalid/leak"
	values["AISUMMONER_OPENCODE_PASSWORD"] = "ignored-provider-password"
	values["AISUMMONER_AGENT_BRIDGE_SECRET"] = "short"
	values["AISUMMONER_AGENT_BRIDGE_LISTEN_ADDR"] = "0.0.0.0:4097"
	values["AISUMMONER_DEEPSEEK_URL"] = "https://provider.invalid"
	values["AISUMMONER_DEEPSEEK_API_KEY"] = "ignored-deepseek-secret"
	values["AISUMMONER_DEEPSEEK_MODEL"] = "ignored-model"

	configuration, err := load(mapLookup(values))
	if err != nil {
		t.Fatalf("load fake config: %v", err)
	}
	if configuration.CookieSecure {
		t.Fatal("development HTTP cookie marked secure")
	}
	if configuration.AllowedOrigin != "http://127.0.0.1:8080" {
		t.Fatalf("unexpected origin: %q", configuration.AllowedOrigin)
	}
	if configuration.DatabasePath == "" || configuration.ListenAddr != defaultListenAddr {
		t.Fatalf("defaults not applied: %#v", configuration)
	}
	if configuration.AgentAdapter != AgentAdapterFake || configuration.OpenCodeURL != "" ||
		configuration.OpenCodePassword != "" || len(configuration.AgentBridgeSecret) != 0 ||
		configuration.AgentBridgeListenAddr != "" || configuration.AgentWorkspaceRoot != "" ||
		configuration.DeepSeekURL != "" || configuration.DeepSeekAPIKey != "" || configuration.DeepSeekModel != "" {
		t.Fatalf("fake mode retained provider-only configuration: %#v", configuration)
	}
}

func TestLoadTrustedProxyIPs(t *testing.T) {
	values := baseValues(t)
	values["AISUMMONER_TRUSTED_PROXY_IPS"] = "127.0.0.1,10.20.0.20,2001:db8::5"
	configuration, err := load(mapLookup(values))
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Addr{
		netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.20.0.20"), netip.MustParseAddr("2001:db8::5"),
	}
	if len(configuration.TrustedProxyIPs) != len(want) {
		t.Fatalf("trusted proxy count = %d", len(configuration.TrustedProxyIPs))
	}
	for index := range want {
		if configuration.TrustedProxyIPs[index] != want[index] {
			t.Fatalf("trusted proxy %d = %s, want %s", index, configuration.TrustedProxyIPs[index], want[index])
		}
	}

	delete(values, "AISUMMONER_TRUSTED_PROXY_IPS")
	direct, err := load(mapLookup(values))
	if err != nil {
		t.Fatal(err)
	}
	if len(direct.TrustedProxyIPs) != 0 {
		t.Fatalf("direct mode retained trusted proxies: %v", direct.TrustedProxyIPs)
	}
}

func TestLoadRejectsInvalidTrustedProxyIPsWithoutEcho(t *testing.T) {
	invalid := []string{
		"proxy.example", "10.20.0.0/24", "fe80::1%eth0", "0.0.0.0", "::", "224.0.0.1", "ff02::1",
		"255.255.255.255", "10.20.0.20,10.20.0.20", "192.0.2.1,::ffff:192.0.2.1",
		"10.20.0.20,", ",10.20.0.20", "10.20.0.20, 10.20.0.21",
	}
	for index, value := range invalid {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			values := baseValues(t)
			values["AISUMMONER_TRUSTED_PROXY_IPS"] = value
			_, err := load(mapLookup(values))
			if err == nil {
				t.Fatal("invalid trusted proxy configuration was accepted")
			}
			if strings.Contains(err.Error(), value) {
				t.Fatalf("configuration error exposed rejected value: %q", err)
			}
		})
	}
}

func TestLoadOpenCodeConfig(t *testing.T) {
	values := baseValues(t)
	values["AISUMMONER_AGENT_ADAPTER"] = AgentAdapterOpenCode
	values["AISUMMONER_OPENCODE_USERNAME"] = "opencode-user"
	values["AISUMMONER_OPENCODE_PASSWORD"] = "opencode-password"
	values["AISUMMONER_OPENCODE_MODEL"] = "free-model"
	values["AISUMMONER_AGENT_BRIDGE_SECRET"] = strings.Repeat("b", minimumSecretSize)
	values["AISUMMONER_DEEPSEEK_URL"] = "http://ignored.invalid"
	values["AISUMMONER_DEEPSEEK_API_KEY"] = "ignored\ninvalid"
	values["AISUMMONER_DEEPSEEK_MODEL"] = "ignored model"

	configuration, err := load(mapLookup(values))
	if err != nil {
		t.Fatalf("load OpenCode config: %v", err)
	}
	if configuration.OpenCodeURL != defaultOpenCodeURL || configuration.OpenCodeBridgeURL != defaultBridgeURL ||
		configuration.AgentBridgeListenAddr != defaultBridgeListenAddr {
		t.Fatalf("unexpected OpenCode defaults: %#v", configuration)
	}
	if configuration.DeepSeekURL != "" || configuration.DeepSeekAPIKey != "" || configuration.DeepSeekModel != "" {
		t.Fatalf("OpenCode mode retained DeepSeek-only configuration: %#v", configuration)
	}
	wantWorkspace := filepath.Join(configuration.DataDir, defaultWorkspaceDir)
	if configuration.AgentWorkspaceRoot != wantWorkspace {
		t.Fatalf("workspace root = %q, want %q", configuration.AgentWorkspaceRoot, wantWorkspace)
	}
}

func TestLoadDeepSeekConfigAndProviderIsolation(t *testing.T) {
	values := baseValues(t)
	values["AISUMMONER_AGENT_ADAPTER"] = AgentAdapterDeepSeek
	values["AISUMMONER_DEEPSEEK_API_KEY"] = "private-provider-key"
	values["AISUMMONER_DEEPSEEK_MODEL"] = "deepseek-v4-flash"
	values["AISUMMONER_OPENCODE_URL"] = "https://ignored.invalid"
	values["AISUMMONER_OPENCODE_PASSWORD"] = "ignored-opencode-secret"
	values["AISUMMONER_AGENT_BRIDGE_SECRET"] = "short"

	configuration, err := load(mapLookup(values))
	if err != nil {
		t.Fatalf("load DeepSeek config: %v", err)
	}
	if configuration.DeepSeekURL != defaultDeepSeekURL || configuration.DeepSeekAPIKey != "private-provider-key" || configuration.DeepSeekModel != "deepseek-v4-flash" {
		t.Fatalf("unexpected DeepSeek config: %#v", configuration)
	}
	if configuration.OpenCodeURL != "" || configuration.OpenCodePassword != "" || configuration.AgentWorkspaceRoot != "" || len(configuration.AgentBridgeSecret) != 0 {
		t.Fatalf("DeepSeek mode retained OpenCode-only configuration: %#v", configuration)
	}
}

func TestLoadDeepSeekConditionalRequirementsAndRedaction(t *testing.T) {
	base := baseValues(t)
	base["AISUMMONER_AGENT_ADAPTER"] = AgentAdapterDeepSeek
	base["AISUMMONER_DEEPSEEK_API_KEY"] = "provider-key-do-not-print"
	base["AISUMMONER_DEEPSEEK_MODEL"] = "deepseek-v4-flash"
	tests := []struct {
		name   string
		change func(map[string]string)
	}{
		{name: "missing key", change: func(values map[string]string) { delete(values, "AISUMMONER_DEEPSEEK_API_KEY") }},
		{name: "missing model", change: func(values map[string]string) { delete(values, "AISUMMONER_DEEPSEEK_MODEL") }},
		{name: "HTTP URL", change: func(values map[string]string) { values["AISUMMONER_DEEPSEEK_URL"] = "http://provider.invalid" }},
		{name: "URL path", change: func(values map[string]string) { values["AISUMMONER_DEEPSEEK_URL"] = "https://provider.invalid/v1" }},
		{name: "URL userinfo", change: func(values map[string]string) { values["AISUMMONER_DEEPSEEK_URL"] = "https://secret@provider.invalid" }},
		{name: "key newline", change: func(values map[string]string) { values["AISUMMONER_DEEPSEEK_API_KEY"] += "\n" }},
		{name: "key internal control", change: func(values map[string]string) { values["AISUMMONER_DEEPSEEK_API_KEY"] = "provider\nkey" }},
		{name: "model whitespace", change: func(values map[string]string) { values["AISUMMONER_DEEPSEEK_MODEL"] = " deepseek-v4-flash" }},
		{name: "model internal control", change: func(values map[string]string) { values["AISUMMONER_DEEPSEEK_MODEL"] = "deepseek\tv4" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := cloneValues(base)
			test.change(values)
			_, err := load(mapLookup(values))
			if err == nil {
				t.Fatal("invalid DeepSeek configuration was accepted")
			}
			for _, forbidden := range []string{"provider-key-do-not-print", "provider.invalid", "deepseek-v4-flash"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("configuration error exposed provider value: %q", err)
				}
			}
		})
	}
}

func TestLoadRejectsInvalidCoreConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]string)
	}{
		{name: "insecure public base URL", change: func(values map[string]string) { values["AISUMMONER_BASE_URL"] = "http://example.com" }},
		{name: "unknown adapter", change: func(values map[string]string) { values["AISUMMONER_AGENT_ADAPTER"] = "OPENCODE" }},
		{name: "invalid public listen", change: func(values map[string]string) { values["AISUMMONER_LISTEN_ADDR"] = "127.0.0.1" }},
		{name: "zero public port", change: func(values map[string]string) { values["AISUMMONER_LISTEN_ADDR"] = "127.0.0.1:0" }},
		{name: "short session secret", change: func(values map[string]string) { values["AISUMMONER_SESSION_SECRET"] = "short" }},
		{name: "short pairing secret", change: func(values map[string]string) { values["AISUMMONER_PAIRING_SECRET"] = "short" }},
		{name: "filesystem root data", change: func(values map[string]string) { values["AISUMMONER_DATA_DIR"] = string(filepath.Separator) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := baseValues(t)
			test.change(values)
			if _, err := load(mapLookup(values)); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestLoadHTTPRequiresLiteralLoopbackListener(t *testing.T) {
	tests := []struct {
		name       string
		baseURL    string
		listenAddr string
		wantError  bool
	}{
		{name: "HTTP IPv4 loopback", baseURL: "http://127.0.0.1:8080", listenAddr: "127.0.0.1:8080"},
		{name: "HTTP IPv6 loopback", baseURL: "http://[::1]:8080", listenAddr: "[::1]:8080"},
		{name: "HTTPS public bind in dev mode", baseURL: "https://example.invalid", listenAddr: "0.0.0.0:8080"},
		{name: "HTTP IPv4 wildcard", baseURL: "http://127.0.0.1:8080", listenAddr: "0.0.0.0:8080", wantError: true},
		{name: "HTTP IPv6 wildcard", baseURL: "http://[::1]:8080", listenAddr: "[::]:8080", wantError: true},
		{name: "HTTP hostname alias", baseURL: "http://127.0.0.1:8080", listenAddr: "localhost:8080", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := baseValues(t)
			values["AISUMMONER_BASE_URL"] = test.baseURL
			values["AISUMMONER_LISTEN_ADDR"] = test.listenAddr
			_, err := load(mapLookup(values))
			if test.wantError && err == nil {
				t.Fatal("unsafe HTTP listener was accepted")
			}
			if !test.wantError && err != nil {
				t.Fatalf("safe listener rejected: %v", err)
			}
		})
	}
}

func TestLoadOpenCodeConditionalRequirements(t *testing.T) {
	valid := openCodeValues(t)
	tests := []struct {
		name   string
		change func(map[string]string)
	}{
		{name: "missing username", change: func(values map[string]string) { delete(values, "AISUMMONER_OPENCODE_USERNAME") }},
		{name: "missing password", change: func(values map[string]string) { delete(values, "AISUMMONER_OPENCODE_PASSWORD") }},
		{name: "missing model", change: func(values map[string]string) { delete(values, "AISUMMONER_OPENCODE_MODEL") }},
		{name: "short bridge secret", change: func(values map[string]string) { values["AISUMMONER_AGENT_BRIDGE_SECRET"] = "short" }},
		{name: "provider hostname", change: func(values map[string]string) { values["AISUMMONER_OPENCODE_URL"] = "http://localhost:4096" }},
		{name: "provider nonloopback", change: func(values map[string]string) { values["AISUMMONER_OPENCODE_URL"] = "https://example.com" }},
		{name: "provider path", change: func(values map[string]string) { values["AISUMMONER_OPENCODE_URL"] = "http://127.0.0.1:4096/api" }},
		{name: "provider userinfo", change: func(values map[string]string) { values["AISUMMONER_OPENCODE_URL"] = "http://user@127.0.0.1:4096" }},
		{name: "public bridge listen", change: func(values map[string]string) { values["AISUMMONER_AGENT_BRIDGE_LISTEN_ADDR"] = "0.0.0.0:4097" }},
		{name: "bridge hostname", change: func(values map[string]string) {
			values["AISUMMONER_OPENCODE_BRIDGE_URL"] = "http://localhost:4097/internal/opencode/remote-exec"
		}},
		{name: "bridge wrong path", change: func(values map[string]string) { values["AISUMMONER_OPENCODE_BRIDGE_URL"] = "http://127.0.0.1:4097/" }},
		{name: "bridge query", change: func(values map[string]string) {
			values["AISUMMONER_OPENCODE_BRIDGE_URL"] = defaultBridgeURL + "?token=x"
		}},
		{name: "bridge authority mismatch", change: func(values map[string]string) { values["AISUMMONER_AGENT_BRIDGE_LISTEN_ADDR"] = "127.0.0.1:4100" }},
		{name: "root workspace", change: func(values map[string]string) { values["AISUMMONER_AGENT_WORKSPACE_ROOT"] = string(filepath.Separator) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := cloneValues(valid)
			test.change(values)
			if _, err := load(mapLookup(values)); err == nil {
				t.Fatal("invalid OpenCode configuration was accepted")
			}
		})
	}
}

func TestLoadErrorsRedactOpenCodeSecrets(t *testing.T) {
	values := openCodeValues(t)
	secret := "bridge-secret-do-not-print-0123456789"
	password := "provider-password-do-not-print"
	values["AISUMMONER_AGENT_BRIDGE_SECRET"] = secret
	values["AISUMMONER_OPENCODE_PASSWORD"] = password
	values["AISUMMONER_OPENCODE_BRIDGE_URL"] = "http://public.invalid/" + secret

	_, err := load(mapLookup(values))
	if err == nil {
		t.Fatal("invalid bridge URL was accepted")
	}
	for _, forbidden := range []string{secret, password, "public.invalid"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("configuration error exposed a secret/value: %q", err)
		}
	}
}

func TestPreparePrivateDirectories(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	workspaceRoot := filepath.Join(root, "workspace")
	configuration := Config{
		DataDir: dataDir, AgentAdapter: AgentAdapterOpenCode, AgentWorkspaceRoot: workspaceRoot,
	}
	if err := configuration.PreparePrivateDirectories(); err != nil {
		t.Fatalf("prepare private directories: %v", err)
	}
	for _, path := range []string{dataDir, workspaceRoot} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("%s mode = %#o, want 0700", path, got)
		}
	}
}

func TestPreparePrivateDirectoriesRejectsExactSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "data-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := (Config{DataDir: link, AgentAdapter: AgentAdapterFake}).PreparePrivateDirectories(); err == nil {
		t.Fatal("exact data-directory symlink was accepted")
	}
}

func TestPreparePrivateDirectoriesRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "parent-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := (Config{DataDir: filepath.Join(link, "data"), AgentAdapter: AgentAdapterFake}).PreparePrivateDirectories(); err == nil {
		t.Fatal("data directory under a symlink parent was accepted")
	}
}

func baseValues(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"AISUMMONER_BASE_URL":       "http://127.0.0.1:8080",
		"AISUMMONER_DEV_MODE":       "1",
		"AISUMMONER_SESSION_SECRET": strings.Repeat("s", minimumSecretSize),
		"AISUMMONER_PAIRING_SECRET": strings.Repeat("p", minimumSecretSize),
		"AISUMMONER_DATA_DIR":       filepath.Join(t.TempDir(), "data"),
	}
}

func openCodeValues(t *testing.T) map[string]string {
	t.Helper()
	values := baseValues(t)
	values["AISUMMONER_AGENT_ADAPTER"] = AgentAdapterOpenCode
	values["AISUMMONER_OPENCODE_URL"] = defaultOpenCodeURL
	values["AISUMMONER_OPENCODE_USERNAME"] = "opencode-user"
	values["AISUMMONER_OPENCODE_PASSWORD"] = "opencode-password"
	values["AISUMMONER_OPENCODE_MODEL"] = "free-model"
	values["AISUMMONER_OPENCODE_BRIDGE_URL"] = defaultBridgeURL
	values["AISUMMONER_AGENT_BRIDGE_LISTEN_ADDR"] = defaultBridgeListenAddr
	values["AISUMMONER_AGENT_BRIDGE_SECRET"] = strings.Repeat("b", minimumSecretSize)
	return values
}

func cloneValues(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func mapLookup(values map[string]string) lookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
