package dsh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareRuntimeHomeInstallsPrivateRemoteOnlyPreset(t *testing.T) {
	home := filepath.Join(t.TempDir(), "private-dsh-home")
	runtimeFiles, err := PrepareRuntimeHome(home)
	if err != nil {
		t.Fatal(err)
	}
	assertPathMode(t, home, 0o700, true)
	assertPathMode(t, filepath.Join(home, ".agent-presets"), 0o700, true)
	assertPathMode(t, filepath.Join(home, ".agent-presets", AgentPreset), 0o700, true)

	expected := []string{
		runtimeFiles.PatchPath,
		filepath.Join(home, ".agent-presets", AgentPreset, "agent.cordis.yml"),
		filepath.Join(home, ".agent-presets", AgentPreset, "preset.yml"),
		filepath.Join(home, ".agent-presets", AgentPreset, "remote-bash.mjs"),
	}
	for _, path := range expected {
		assertPathMode(t, path, 0o600, false)
	}
	for _, path := range []string{
		filepath.Join(home, ".agent-presets", WindowsAgentPreset, "agent.cordis.yml"),
		filepath.Join(home, ".agent-presets", WindowsAgentPreset, "preset.yml"),
		filepath.Join(home, ".agent-presets", WindowsAgentPreset, "remote-bash.mjs"),
	} {
		assertPathMode(t, path, 0o600, false)
	}

	composition := readAsset(t, expected[1])
	for _, forbidden := range []string{
		"@deepseek-ai/dsh-tools-shell",
		"@deepseek-ai/dsh-tools-filesystem",
		"@deepseek-ai/dsh-web-search",
		"@deepseek-ai/dsh-subagent",
	} {
		if strings.Contains(composition, forbidden) {
			t.Fatalf("remote-only composition contains %q", forbidden)
		}
	}
	if strings.Count(composition, "name: ./remote-bash.mjs") != 1 || !strings.Contains(composition, "complete: true") {
		t.Fatalf("unexpected DSH composition: %q", composition)
	}

	tool := readAsset(t, expected[3])
	for _, required := range []string{
		"name: 'bash'",
		CallbackPath,
		ProofDomain,
		EnvBridgeURL,
		EnvBridgeSecret,
		"redirect: 'error'",
	} {
		if !strings.Contains(tool, required) {
			t.Fatalf("remote tool is missing %q", required)
		}
	}
	for _, forbidden := range []string{"child_process", "execSync", "spawn(", "device_id"} {
		if strings.Contains(tool, forbidden) {
			t.Fatalf("remote tool contains forbidden local/device selector %q", forbidden)
		}
	}

	credentialSentinel := filepath.Join(home, ".credentials.yaml")
	sessionSentinel := filepath.Join(home, "sessions", "keep.json")
	if err := os.MkdirAll(filepath.Dir(sessionSentinel), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialSentinel, []byte("credential-sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionSentinel, []byte("session-sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareRuntimeHome(home); err != nil {
		t.Fatal(err)
	}
	if got := readAsset(t, credentialSentinel); got != "credential-sentinel" {
		t.Fatalf("credential store was changed: %q", got)
	}
	if got := readAsset(t, sessionSentinel); got != "session-sentinel" {
		t.Fatalf("session store was changed: %q", got)
	}
}

func TestPrepareRuntimeHomeRejectsSymlinksAndBroadTargets(t *testing.T) {
	if _, err := PrepareRuntimeHome("/"); err == nil {
		t.Fatal("root DSH home was accepted")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-home")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareRuntimeHome(link); err == nil {
		t.Fatal("symlink DSH home was accepted")
	}
}

func assertPathMode(t *testing.T, path string, mode os.FileMode, directory bool) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || info.Mode().Perm() != mode {
		t.Fatalf("path %q mode=%v directory=%v", path, info.Mode(), info.IsDir())
	}
}

func readAsset(t *testing.T, path string) string {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}
