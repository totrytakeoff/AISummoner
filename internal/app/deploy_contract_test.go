package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This static oracle protects security-critical deployment topology even on
// hosts where Docker is unavailable. Task008 still runs `compose config` and
// image builds separately when the resource gate permits.
func TestDeploymentContractSources(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(relative string) string {
		t.Helper()
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		return string(contents)
	}
	compose := read("deploy/compose.yaml")
	for _, required := range []string{
		`profiles: ["opencode"]`, `network_mode: "service:server"`,
		`image: aisummoner-opencode:1.18.11`,
		`condition: service_started`,
		`user: "10001:10001"`,
		`server-data:/var/lib/aisummoner/data`, `agent-workspaces:/var/lib/aisummoner/workspaces`,
		`["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/healthz"]`,
		`AISUMMONER_TRUSTED_PROXY_IPS: "${AISUMMONER_CADDY_EDGE_IP:-172.30.0.20}"`,
		`AISUMMONER_DEEPSEEK_URL: "${AISUMMONER_DEEPSEEK_URL:-https://api.deepseek.com}"`,
		`AISUMMONER_DEEPSEEK_API_KEY: "${AISUMMONER_DEEPSEEK_API_KEY:-}"`,
		`AISUMMONER_DEEPSEEK_MODEL: "${AISUMMONER_DEEPSEEK_MODEL:-}"`,
		`ipv4_address: "${AISUMMONER_SERVER_EDGE_IP:-172.30.0.10}"`,
		`ipv4_address: "${AISUMMONER_CADDY_EDGE_IP:-172.30.0.20}"`,
		`subnet: "${AISUMMONER_EDGE_SUBNET:-172.30.0.0/24}"`,
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("Compose source lacks contract %q", required)
		}
	}
	if strings.Contains(compose, "AISUMMONER_OPENCODE_IMAGE") {
		t.Fatal("Compose must not expose an unverified OpenCode image override")
	}
	if got := strings.Count(compose, "\n    ports:\n"); got != 1 {
		t.Fatalf("Compose published-port sections = %d, want exactly Caddy's one section", got)
	}
	if got := strings.Count(compose, "server-data:/var/lib/aisummoner/data"); got != 1 {
		t.Fatalf("Server data volume mounts = %d, want Server only", got)
	}
	if got := strings.Count(compose, "agent-workspaces:/var/lib/aisummoner/workspaces"); got != 2 {
		t.Fatalf("workspace mounts = %d, want Server and OpenCode", got)
	}
	if got := strings.Count(compose, "AISUMMONER_TRUSTED_PROXY_IPS:"); got != 1 {
		t.Fatalf("trusted-proxy injections = %d, want Server only", got)
	}
	if got := strings.Count(compose, "      AISUMMONER_DEEPSEEK_API_KEY:"); got != 1 {
		t.Fatalf("DeepSeek credential injections = %d, want Server only", got)
	}
	if got := strings.Count(compose, "ipv4_address:"); got != 2 {
		t.Fatalf("deterministic edge addresses = %d, want Server and Caddy", got)
	}
	caddyfile := read("deploy/Caddyfile")
	if got := strings.Count(caddyfile, "header_up X-AISummoner-Client-IP {remote_host}"); got != 1 {
		t.Fatalf("Caddy dedicated source-header overwrite count = %d, want 1", got)
	}
	if strings.Contains(caddyfile, "header_up +X-AISummoner-Client-IP") {
		t.Fatal("Caddy must overwrite, not append, the dedicated source header")
	}
	openCodeDockerfile := read("deploy/OpenCode.Dockerfile")
	for _, required := range []string{"node:22.18.0-bookworm-slim", "OPENCODE_VERSION=1.18.11", `opencode-ai@${OPENCODE_VERSION}`, `opencode --version`} {
		if !strings.Contains(openCodeDockerfile, required) {
			t.Fatalf("OpenCode Dockerfile lacks pinned contract %q", required)
		}
	}
	serverDockerfile := read("deploy/Dockerfile")
	for _, required := range []string{"npm --prefix web ci", "COPY web/src ./web/src", "npm --prefix web test -- --run", "npm --prefix web run build", "rm -rf ./internal/staticweb/assets", "! grep -q"} {
		if !strings.Contains(serverDockerfile, required) {
			t.Fatalf("Server Dockerfile lacks production embed contract %q", required)
		}
	}
	if strings.Contains(serverDockerfile, "COPY web ./web") {
		t.Fatal("Server Dockerfile must copy explicit Web source/config, not the entire local Web tree")
	}
	dockerignore := read(".dockerignore")
	for _, required := range []string{
		".git", ".env.*", "!.env.example", "**/data/**", "*.db", "*.db-*",
		"*.log", "*.key", "*.pem", "web/node_modules", "web/dist", "dist/**", "bin/**", "*.test",
	} {
		if !strings.Contains(dockerignore, required) {
			t.Fatalf(".dockerignore lacks sensitive/generated boundary %q", required)
		}
	}
	validation := read("deploy/validate-compose.sh")
	for _, required := range []string{"COMPOSE_PROFILES", "fixed local aisummoner-opencode:1.18.11", "opencode adapter requires", "fake adapter must not enable", "deepseek adapter must not enable", "deepseek:0", `config --quiet`} {
		if !strings.Contains(validation, required) {
			t.Fatalf("Compose validation lacks contract %q", required)
		}
	}
	if strings.Contains(validation, `config "$@"`) || strings.Contains(validation, `config "$`) {
		t.Fatal("Compose validation must not allow callers to render interpolated secrets")
	}
	environmentExample := read(".env.example")
	for _, required := range []string{
		"AISUMMONER_DEEPSEEK_URL=https://api.deepseek.com",
		"AISUMMONER_DEEPSEEK_API_KEY=\n",
		"AISUMMONER_DEEPSEEK_MODEL=\n",
	} {
		if !strings.Contains(environmentExample, required) {
			t.Fatalf(".env.example lacks safe DeepSeek contract %q", required)
		}
	}
	readme := read("README.md")
	if got := strings.Count(readme, "install -m 600 .env.example .env"); got < 2 {
		t.Fatalf("README private .env creation instructions = %d, want local and Compose guidance", got)
	}
	if !strings.Contains(readme, `test "$(stat -c '%a' .env)" = 600`) {
		t.Fatal("README must verify .env mode before Compose starts")
	}
	if strings.Contains(readme, "cp .env.example .env") {
		t.Fatal("README must not recommend copying secrets into a potentially world-readable .env")
	}
	clientUnit := read("deploy/aisummoner-client.service")
	for _, required := range []string{
		"StateDirectory=aisummoner-client", "StateDirectoryMode=0700", "UMask=0077",
		"StandardOutput=append:/var/lib/aisummoner-client/pairing-output.log", "StandardError=journal",
	} {
		if !strings.Contains(clientUnit, required) {
			t.Fatalf("Remote Client unit lacks private pairing-output contract %q", required)
		}
	}
	if strings.Contains(clientUnit, "StandardOutput=journal") {
		t.Fatal("Remote Client unit must never persist pairing codes in the journal")
	}
}
