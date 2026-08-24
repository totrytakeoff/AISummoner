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
	dshPackager := read("deploy/package-dsh-runtime.sh")
	for _, required := range []string{
		"47f943859bef60e4160492346772ded9b24f765a",
		"node_version=24.19.0",
		"14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647",
		"pnpm_version=11.7.0",
		"--frozen-lockfile", "--offline --trust-lockfile", "--child-concurrency=1", "--max-old-space-size=2048",
		"dist/node_modules/node-gyp/bin/node-gyp.js", "\"$node\" \"$node_gyp\" rebuild", "node-pty/build/Release/pty.node",
		"node-pty portable build output is missing",
		"install -m 0755 \"$node_pty_binary\" \"$node_pty_destination\"",
		"npm_config_nodedir=$bundle/node",
		"--filter @deepseek-ai/dsh --prod deploy --offline --ignore-scripts --trust-lockfile",
		"--config.inject-workspace-packages=true",
		"python/sdk-runtime/package.json",
		"materialize-dsh-runtime.mjs", "MemAvailable", "runtime-files.sha256",
	} {
		if !strings.Contains(dshPackager, required) {
			t.Fatalf("DSH runtime packager lacks pinned/private contract %q", required)
		}
	}
	if strings.Contains(dshPackager, "rm -rf") {
		t.Fatal("DSH runtime packager must clean only its exact private staging tree")
	}
	dshContainerPackager := read("deploy/package-dsh-runtime-container.sh")
	for _, required := range []string{
		"node@sha256:934240a162082fd8b8a2f90cd5114446443f1eba1c5378f6687167ca405e6584",
		"--cpus=2", "--memory=3g", "--memory-swap=4g", "--cap-drop=ALL",
		"--security-opt no-new-privileges", "--user", "package-dsh-runtime.sh",
	} {
		if !strings.Contains(dshContainerPackager, required) {
			t.Fatalf("DSH container packager lacks portable build contract %q", required)
		}
	}
	dshChecker := read("deploy/check-dsh-runtime.sh")
	for _, required := range []string{
		"sha256sum --quiet -c runtime-files.sha256", "v24.19.0", "0.1.0-rc.5",
		"node-pty", "koffi", "sharp", "type l", "Landlock runtime binary",
	} {
		if !strings.Contains(dshChecker, required) {
			t.Fatalf("DSH runtime checker lacks closure contract %q", required)
		}
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
		"AISUMMONER_DSH_URL=http://127.0.0.1:14096",
		"AISUMMONER_DSH_NODE_PATH=/opt/aisummoner/dsh/node/bin/node",
		"AISUMMONER_DSH_CLI_PATH=/opt/aisummoner/dsh/runtime/lib/bin.js",
		"AISUMMONER_DSH_BRIDGE_URL=http://127.0.0.1:14097/internal/dsh/remote-exec",
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
		"StandardOutput=null", "StandardError=journal",
		"aisummoner-client daemon --server ${AISUMMONER_SERVER_URL} --data-dir /var/lib/aisummoner-client",
	} {
		if !strings.Contains(clientUnit, required) {
			t.Fatalf("Remote Client unit lacks daemon/private-IPC contract %q", required)
		}
	}
	for _, forbidden := range []string{"pairing-output.log", "aisummoner-client start ", "StandardOutput=journal"} {
		if strings.Contains(clientUnit, forbidden) {
			t.Fatalf("Remote Client unit retains obsolete pairing-output contract %q", forbidden)
		}
	}
}
