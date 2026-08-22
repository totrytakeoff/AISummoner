package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aisummoner/aisummoner/internal/opencodebridge"
)

func TestWorkspaceCreatesPrivateOpaqueExactAllowlist(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	manager, err := newWorkspaceManager(root)
	if err != nil {
		t.Fatal(err)
	}
	const productSessionID = "ags_secret_product_session"
	workspace, err := manager.prepare(productSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(workspace, productSessionID) {
		t.Fatalf("workspace leaked raw session id: %s", workspace)
	}
	if again, err := manager.prepare(productSessionID); err != nil || again != workspace {
		t.Fatalf("verified reuse=%q err=%v", again, err)
	}
	for _, directory := range []string{root, workspace, filepath.Join(workspace, ".opencode"), filepath.Join(workspace, ".opencode", "tools")} {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("private directory %s info=%v err=%v", directory, info, err)
		}
	}
	for relative := range allowedWorkspaceFiles {
		info, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(relative)))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("private file %s info=%v err=%v", relative, info, err)
		}
	}
}

func TestWorkspaceReuseRejectsMissingAlteredExtraAndSymlink(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string) error
	}{
		{name: "missing file", mutate: func(workspace string) error { return os.Remove(filepath.Join(workspace, configRelativePath)) }},
		{name: "missing tools directory", mutate: func(workspace string) error { return os.RemoveAll(filepath.Join(workspace, ".opencode", "tools")) }},
		{name: "altered", mutate: func(workspace string) error {
			return os.WriteFile(filepath.Join(workspace, configRelativePath), []byte("{}"), 0o600)
		}},
		{name: "extra", mutate: func(workspace string) error {
			return os.WriteFile(filepath.Join(workspace, "extra"), []byte("x"), 0o600)
		}},
		{name: "symlink", mutate: func(workspace string) error { return os.Symlink("opencode.json", filepath.Join(workspace, "link")) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, err := newWorkspaceManager(filepath.Join(t.TempDir(), "workspaces"))
			if err != nil {
				t.Fatal(err)
			}
			workspace, err := manager.prepare("ags_" + strings.ReplaceAll(test.name, " ", "_"))
			if err != nil {
				t.Fatal(err)
			}
			if err := test.mutate(workspace); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.prepare("ags_" + strings.ReplaceAll(test.name, " ", "_")); err == nil {
				t.Fatal("altered workspace was silently repaired")
			}
		})
	}
}

func TestWorkspaceRejectsSymlinkRootComponent(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	if _, err := newWorkspaceManager(filepath.Join(link, "workspaces")); err == nil {
		t.Fatal("symlinked workspace path accepted")
	}
}

func TestEmbeddedAndRootTemplatesAreCanonicalAndDenyLocalTools(t *testing.T) {
	for relative, assetPath := range allowedWorkspaceFiles {
		embedded, err := templateFiles.ReadFile(assetPath)
		if err != nil {
			t.Fatal(err)
		}
		rootFile, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if !equalBytes(embedded, rootFile) {
			t.Fatalf("root and embedded %s differ", relative)
		}
	}
	config, err := templateFiles.ReadFile(allowedWorkspaceFiles[configRelativePath])
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Permission map[string]string `json:"permission"`
	}
	if json.Unmarshal(config, &policy) != nil || policy.Permission["*"] != "deny" || policy.Permission["remote_exec"] != "allow" || len(policy.Permission) != 2 {
		t.Fatalf("unsafe OpenCode policy: %s", config)
	}
	toolSource, err := templateFiles.ReadFile(allowedWorkspaceFiles[toolRelativePath])
	if err != nil {
		t.Fatal(err)
	}
	source := string(toolSource)
	for _, required := range []string{opencodebridge.EnvBridgeURL, opencodebridge.EnvBridgeSecret, opencodebridge.Authorization, "context.sessionID", "context.abort", "timeout_seconds", "exit_code", "truncated", "denied", "failure_code"} {
		if !strings.Contains(source, required) {
			t.Fatalf("tool contract lacks %q", required)
		}
	}
	for _, forbidden := range []string{"child_process", "Bun.spawn", "Deno.Command", "node:fs", "device_id", "user_id"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("tool source contains forbidden capability %q", forbidden)
		}
	}
}
