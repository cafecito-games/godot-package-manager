package installer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/source"
)

// Install resolves the source subtree within fetched.Dir and copies it into
// <addonsDir>/<spec.InstallName()>, replacing any existing directory atomically.
// If copying fails partway, the existing addon is left untouched (rollback-safe).
func Install(fetched source.FetchResult, spec manifest.AddonSpec, addonsDir string) error {
	sourceRoot, err := resolveSourcePath(fetched.Dir, spec.SourcePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(addonsDir, 0o755); err != nil {
		return &output.InstallError{Err: err}
	}

	destination := filepath.Join(addonsDir, spec.InstallName())
	if err := containmentCheck(addonsDir, destination); err != nil {
		return err
	}

	// Copy into a staging directory on the same filesystem so the final rename is atomic.
	staging, err := os.MkdirTemp(addonsDir, ".gpm-staging-*")
	if err != nil {
		return &output.InstallError{Err: err}
	}

	if err := copyTree(sourceRoot, staging); err != nil {
		_ = os.RemoveAll(staging)
		return &output.InstallError{Err: err}
	}

	// Atomically swap staging into place, preserving the old directory as a
	// backup so we can restore it if the rename of staging fails.
	_, statErr := os.Stat(destination)
	destinationExists := statErr == nil

	if destinationExists {
		backupPath := destination + ".gpm-backup"
		if err := os.Rename(destination, backupPath); err != nil {
			_ = os.RemoveAll(staging)
			return &output.InstallError{Err: fmt.Errorf("could not back up existing addon: %w", err)}
		}
		if err := os.Rename(staging, destination); err != nil {
			// Restore the backup so the addon is not lost.
			_ = os.Rename(backupPath, destination)
			_ = os.RemoveAll(staging)
			return &output.InstallError{Err: fmt.Errorf("could not move staged addon into place: %w", err)}
		}
		_ = os.RemoveAll(backupPath)
	} else {
		if err := os.Rename(staging, destination); err != nil {
			_ = os.RemoveAll(staging)
			return &output.InstallError{Err: fmt.Errorf("could not move staged addon into place: %w", err)}
		}
	}

	return nil
}

// containmentCheck returns an error if destination is not strictly inside
// addonsDir, preventing path-traversal via crafted install_as values.
func containmentCheck(addonsDir, destination string) error {
	cleanAddons := filepath.Clean(addonsDir)
	cleanDestination := filepath.Clean(destination)
	if !strings.HasPrefix(cleanDestination, cleanAddons+string(os.PathSeparator)) {
		return &output.InstallError{Err: fmt.Errorf(
			"install destination %q escapes addons directory %q", destination, addonsDir)}
	}
	return nil
}

// resolveSourcePath returns the directory within root to install. When
// sourcePath is set it is used directly. Otherwise a single addons/<name>/
// directory is auto-detected; if no addons/ dir exists, root is used.
func resolveSourcePath(root, sourcePath string) (string, error) {
	if sourcePath != "" {
		resolved := filepath.Join(root, sourcePath)
		cleanRoot := filepath.Clean(root)
		cleanResolved := filepath.Clean(resolved)
		if cleanResolved != cleanRoot && !strings.HasPrefix(cleanResolved, cleanRoot+string(os.PathSeparator)) {
			return "", &output.InstallError{Err: fmt.Errorf("source_path %q escapes the fetched source root", sourcePath)}
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", &output.InstallError{Err: fmt.Errorf("source_path %q in fetched source: %w", sourcePath, err)}
		}
		if !info.IsDir() {
			return "", &output.InstallError{Err: fmt.Errorf("source_path %q is not a directory", sourcePath)}
		}
		return resolved, nil
	}
	addonsDir := filepath.Join(root, "addons")
	entries, err := os.ReadDir(addonsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No addons/ directory: install from the fetched root.
			return root, nil
		}
		return "", &output.InstallError{Err: fmt.Errorf("reading addons directory: %w", err)}
	}
	var directories []string
	for _, entry := range entries {
		if entry.IsDir() {
			directories = append(directories, entry.Name())
		}
	}
	switch len(directories) {
	case 1:
		return filepath.Join(addonsDir, directories[0]), nil
	case 0:
		return root, nil
	default:
		return "", &output.InstallError{Err: fmt.Errorf(
			"fetched source has multiple directories under addons/; set source_path explicitly")}
	}
}

// copyTree recursively copies the directory src to dst.
// Symlinks anywhere in the tree are rejected to prevent host-file exposure.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return &output.InstallError{Err: fmt.Errorf(
				"addon source contains an unsupported symlink: %s", entry.Name())}
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o200)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}
