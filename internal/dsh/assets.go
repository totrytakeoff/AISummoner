package dsh

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed assets/runtime.patch.yml assets/aisummoner/agent.cordis.yml assets/aisummoner/preset.yml assets/aisummoner/remote-bash.mjs assets/aisummoner-windows/agent.cordis.yml assets/aisummoner-windows/preset.yml assets/aisummoner-windows/remote-bash.mjs
var runtimeAssets embed.FS

type RuntimeFiles struct {
	PatchPath string
}

// PrepareRuntimeHome installs the exact reviewed preset and profile overlay
// into a private DSH home. DSH-owned sessions and credentials are preserved.
func PrepareRuntimeHome(home string) (RuntimeFiles, error) {
	if !filepath.IsAbs(home) || filepath.Clean(home) == string(filepath.Separator) {
		return RuntimeFiles{}, errors.New("DSH home must be an absolute private directory")
	}
	home = filepath.Clean(home)
	if err := ensurePrivateDirectory(home); err != nil {
		return RuntimeFiles{}, err
	}
	presetRoot := filepath.Join(home, ".agent-presets")
	presetDirs := []string{filepath.Join(presetRoot, AgentPreset), filepath.Join(presetRoot, WindowsAgentPreset)}
	for _, directory := range append([]string{presetRoot}, presetDirs...) {
		if err := ensurePrivateDirectory(directory); err != nil {
			return RuntimeFiles{}, err
		}
	}
	files := []struct {
		embedded string
		target   string
	}{
		{"assets/aisummoner/agent.cordis.yml", filepath.Join(presetDirs[0], "agent.cordis.yml")},
		{"assets/aisummoner/preset.yml", filepath.Join(presetDirs[0], "preset.yml")},
		{"assets/aisummoner/remote-bash.mjs", filepath.Join(presetDirs[0], "remote-bash.mjs")},
		{"assets/aisummoner-windows/agent.cordis.yml", filepath.Join(presetDirs[1], "agent.cordis.yml")},
		{"assets/aisummoner-windows/preset.yml", filepath.Join(presetDirs[1], "preset.yml")},
		{"assets/aisummoner-windows/remote-bash.mjs", filepath.Join(presetDirs[1], "remote-bash.mjs")},
		{"assets/runtime.patch.yml", filepath.Join(home, ".aisummoner-runtime.patch.yml")},
	}
	for _, file := range files {
		content, err := runtimeAssets.ReadFile(file.embedded)
		if err != nil {
			return RuntimeFiles{}, errors.New("read embedded DSH runtime asset")
		}
		if err := writePrivateFile(file.target, content); err != nil {
			return RuntimeFiles{}, err
		}
	}
	return RuntimeFiles{PatchPath: files[len(files)-1].target}, nil
}

func ensurePrivateDirectory(path string) error {
	if err := rejectSymlinkPath(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("DSH runtime path must be a real directory")
		}
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(path, 0o700); err != nil {
			return errors.New("create DSH runtime directory")
		}
	default:
		return errors.New("inspect DSH runtime directory")
	}
	if err := rejectSymlinkPath(path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return errors.New("secure DSH runtime directory")
	}
	info, err = os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("DSH runtime directory is not private")
	}
	return nil
}

func rejectSymlinkPath(value string) error {
	for current := filepath.Clean(value); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("DSH runtime path contains a symlink")
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return errors.New("inspect DSH runtime path")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func writePrivateFile(path string, content []byte) error {
	parent := filepath.Dir(path)
	if err := ensurePrivateDirectory(parent); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".aisummoner-dsh-*")
	if err != nil {
		return errors.New("create private DSH runtime asset")
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return errors.New("secure private DSH runtime asset")
	}
	if _, err := temporary.Write(content); err != nil {
		return errors.New("write private DSH runtime asset")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync private DSH runtime asset")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close private DSH runtime asset")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errors.New("install private DSH runtime asset")
	}
	committed = true
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return errors.New("DSH runtime asset is not a private regular file")
	}
	return nil
}
