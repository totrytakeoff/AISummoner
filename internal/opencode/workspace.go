package opencode

import (
	"crypto/sha256"
	"embed"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	configRelativePath = "opencode.json"
	toolRelativePath   = ".opencode/tools/remote_exec.ts"
)

//go:embed assets/opencode.json assets/.opencode/tools/remote_exec.ts
var templateFiles embed.FS

var allowedWorkspaceFiles = map[string]string{
	configRelativePath: "assets/opencode.json",
	toolRelativePath:   "assets/.opencode/tools/remote_exec.ts",
}

type workspaceManager struct {
	root string
	mu   sync.Mutex
}

func newWorkspaceManager(root string) (*workspaceManager, error) {
	if root == "" || strings.IndexByte(root, 0) >= 0 {
		return nil, errors.New("opencode workspace root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve opencode workspace root: %w", err)
	}
	if err := rejectSymlinkComponents(absolute); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(absolute, true); err != nil {
		return nil, err
	}
	return &workspaceManager{root: absolute}, nil
}

func (manager *workspaceManager) prepare(productSessionID string) (string, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if productSessionID == "" || strings.IndexByte(productSessionID, 0) >= 0 {
		return "", errors.New("invalid product session id")
	}
	hash := sha256.Sum256([]byte(productSessionID))
	directoryName := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(hash[:]))
	workspace := filepath.Join(manager.root, "session-"+directoryName)
	if err := rejectSymlinkComponents(manager.root); err != nil {
		return "", err
	}
	if err := ensurePrivateDirectory(manager.root, false); err != nil {
		return "", err
	}
	_, inspectErr := os.Lstat(workspace)
	if inspectErr == nil {
		// Reuse is verification-only. Missing or altered entries are evidence of
		// an untrusted workspace and are never repaired in place.
		if err := ensurePrivateDirectory(workspace, false); err != nil {
			return "", err
		}
		if err := verifyWorkspaceAllowlist(workspace); err != nil {
			return "", err
		}
		return workspace, nil
	}
	if !errors.Is(inspectErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect opencode workspace: %w", inspectErr)
	}
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return "", fmt.Errorf("create private opencode workspace: %w", err)
	}
	if err := ensurePrivateDirectory(workspace, false); err != nil {
		return "", err
	}
	for _, directory := range []string{filepath.Join(workspace, ".opencode"), filepath.Join(workspace, ".opencode", "tools")} {
		if err := ensurePrivateDirectory(directory, true); err != nil {
			return "", err
		}
	}
	for relativePath, assetPath := range allowedWorkspaceFiles {
		contents, err := templateFiles.ReadFile(assetPath)
		if err != nil {
			return "", fmt.Errorf("read embedded opencode template: %w", err)
		}
		if err := ensureExactFile(filepath.Join(workspace, filepath.FromSlash(relativePath)), contents); err != nil {
			return "", err
		}
	}
	if err := verifyWorkspaceAllowlist(workspace); err != nil {
		return "", err
	}
	return workspace, nil
}

func rejectSymlinkComponents(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("opencode workspace path contains a symlink")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect opencode workspace path: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func ensurePrivateDirectory(path string, create bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create private opencode directory: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect private opencode directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("opencode workspace directory is not a private real directory")
	}
	return nil
}

func ensureExactFile(path string, expected []byte) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		file, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return fmt.Errorf("create opencode workspace file: %w", createErr)
		}
		written, writeErr := file.Write(expected)
		closeErr := file.Close()
		if writeErr != nil || written != len(expected) {
			return errors.New("write opencode workspace file")
		}
		if closeErr != nil {
			return fmt.Errorf("close opencode workspace file: %w", closeErr)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect opencode workspace file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("opencode workspace file is not a private regular file")
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read opencode workspace file: %w", err)
	}
	if !equalBytes(actual, expected) {
		return errors.New("opencode workspace template differs from reviewed content")
	}
	return nil
}

func verifyWorkspaceAllowlist(workspace string) error {
	allowedDirectories := map[string]bool{".": true, ".opencode": true, ".opencode/tools": true}
	seenDirectories := make(map[string]bool, len(allowedDirectories))
	seenFiles := make(map[string]bool, len(allowedWorkspaceFiles))
	err := filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("opencode workspace contains a symlink")
		}
		if entry.IsDir() {
			if !allowedDirectories[relative] || info.Mode().Perm() != 0o700 {
				return errors.New("opencode workspace contains an unexpected directory")
			}
			seenDirectories[relative] = true
			return nil
		}
		assetPath, allowed := allowedWorkspaceFiles[relative]
		if !allowed || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return errors.New("opencode workspace contains an unexpected file")
		}
		expected, err := templateFiles.ReadFile(assetPath)
		if err != nil {
			return fmt.Errorf("read embedded opencode template: %w", err)
		}
		actual, err := os.ReadFile(path)
		if err != nil || !equalBytes(actual, expected) {
			return errors.New("opencode workspace template differs from reviewed content")
		}
		seenFiles[relative] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seenDirectories) != len(allowedDirectories) || len(seenFiles) != len(allowedWorkspaceFiles) {
		return errors.New("opencode workspace allowlist is incomplete")
	}
	return nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
