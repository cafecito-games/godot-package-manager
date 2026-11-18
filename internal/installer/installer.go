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
	"github.com/cafecito-games/godot-package-manager/internal/symlink"
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

	backupPath := destination + ".gpm-backup"
	if err := recoverBackup(destination, backupPath); err != nil {
		return err
	}

	// Copy into a staging directory on the same filesystem so the final rename is atomic.
	staging, err := os.MkdirTemp(addonsDir, ".gpm-staging-*")
	if err != nil {
		return &output.InstallError{Err: err}
	}

	if err := copyTree(sourceRoot, staging, spec.Exclude); err != nil {
		_ = os.RemoveAll(staging)
		return wrapInstallError(err)
	}

	// Atomically swap staging into place, preserving the old directory as a
	// backup so we can restore it if the rename of staging fails.
	_, statErr := os.Stat(destination)
	destinationExists := statErr == nil

	if destinationExists {
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

// recoverBackup repairs the state left by a process that crashed mid-swap. If a
// leftover `<destination>.gpm-backup` exists, it is restored when destination
// is missing (the backup is the only surviving copy) or removed when
// destination is present (the backup is stale).
func recoverBackup(destination, backupPath string) error {
	if _, err := os.Lstat(backupPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return &output.InstallError{Err: err}
	}
	if _, err := os.Stat(destination); errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(backupPath, destination); err != nil {
			return &output.InstallError{Err: fmt.Errorf("restoring interrupted addon backup: %w", err)}
		}
		return nil
	}
	if err := os.RemoveAll(backupPath); err != nil {
		return &output.InstallError{Err: err}
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
	cleanRoot := filepath.Clean(root)
	if sourcePath != "" {
		resolved := filepath.Join(cleanRoot, sourcePath)
		cleanResolved := filepath.Clean(resolved)
		if cleanResolved != cleanRoot && !strings.HasPrefix(cleanResolved, cleanRoot+string(os.PathSeparator)) {
			return "", &output.InstallError{Err: fmt.Errorf("source_path %q escapes the fetched source root", sourcePath)}
		}
		if err := requireNoSymlinkBetween(cleanRoot, cleanResolved, sourcePath); err != nil {
			return "", err
		}
		info, err := os.Lstat(cleanResolved)
		if err != nil {
			return "", &output.InstallError{Err: fmt.Errorf("source_path %q in fetched source: %w", sourcePath, err)}
		}
		if !info.IsDir() {
			return "", &output.InstallError{Err: fmt.Errorf("source_path %q is not a directory", sourcePath)}
		}
		return cleanResolved, nil
	}
	addonsDir := filepath.Join(cleanRoot, "addons")
	addonsInfo, err := os.Lstat(addonsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No addons/ directory: install from the fetched root.
			return cleanRoot, nil
		}
		return "", &output.InstallError{Err: fmt.Errorf("reading addons directory: %w", err)}
	}
	if addonsInfo.Mode()&os.ModeSymlink != 0 {
		return "", &output.InstallError{Err: fmt.Errorf("addon source contains an unsupported symlink: addons")}
	}
	entries, err := os.ReadDir(addonsDir)
	if err != nil {
		return "", &output.InstallError{Err: fmt.Errorf("reading addons directory: %w", err)}
	}
	var directories []string
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return "", &output.InstallError{Err: fmt.Errorf(
				"addon source contains an unsupported symlink: %s", filepath.Join("addons", entry.Name()))}
		}
		if entry.IsDir() {
			directories = append(directories, entry.Name())
		}
	}
	switch len(directories) {
	case 1:
		return filepath.Join(addonsDir, directories[0]), nil
	case 0:
		return cleanRoot, nil
	default:
		return "", &output.InstallError{Err: fmt.Errorf(
			"fetched source has multiple directories under addons/; set source_path explicitly")}
	}
}

func requireNoSymlinkBetween(cleanRoot, cleanTarget, rawSourcePath string) error {
	relative, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil || relative == "." {
		return nil
	}
	traversed := cleanRoot
	for _, component := range strings.Split(relative, string(os.PathSeparator)) {
		traversed = filepath.Join(traversed, component)
		info, err := os.Lstat(traversed)
		if err != nil {
			return &output.InstallError{Err: fmt.Errorf("source_path %q in fetched source: %w", rawSourcePath, err)}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return &output.InstallError{Err: fmt.Errorf(
				"addon source contains an unsupported symlink: %s", component)}
		}
	}
	return nil
}

// copyTree recursively copies source to destination, materializing relative
// symlinks whose targets stay within destination and rejecting symlinks whose
// targets are absolute or escape destination.
func copyTree(source, destination string, excludeDirs []string) error {
	excludeSet := make(map[string]struct{}, len(excludeDirs))
	for _, excludeDir := range excludeDirs {
		excludeSet[cleanExcludeDir(excludeDir)] = struct{}{}
	}
	symlinks := symlink.NewTree()

	walkErr := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		relative = filepath.Clean(relative)
		if relative == "." {
			if entry.Type()&os.ModeSymlink != 0 {
				return unsupportedSourceSymlinkError(entry.Name(), fmt.Errorf("addon root must not be a symlink"))
			}
			return os.MkdirAll(destination, 0o755)
		}
		if entry.IsDir() {
			if _, excluded := excludeSet[relative]; excluded {
				return filepath.SkipDir
			}
		}
		relativeSlash := filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			rawTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			normalizedTarget, err := symlink.NormalizeFilesystemTarget(rawTarget)
			if err != nil {
				return unsupportedSourceSymlinkError(relativeSlash, err)
			}
			if err := symlinks.AddSymlink(relativeSlash, normalizedTarget); err != nil {
				return unsupportedSourceSymlinkError(relativeSlash, err)
			}
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			if err := symlinks.AddDirectory(relativeSlash); err != nil {
				return unsupportedSourceSymlinkError(relativeSlash, err)
			}
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return &output.InstallError{Err: fmt.Errorf(
				"addon source contains an unsupported file type: %s", relativeSlash)}
		}
		if err := symlinks.AddFile(relativeSlash); err != nil {
			return unsupportedSourceSymlinkError(relativeSlash, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target, info.Mode())
	})
	if walkErr != nil {
		return walkErr
	}
	if err := symlinks.Materialize(destination); err != nil {
		return unsupportedSourceSymlinkError("", err)
	}
	return nil
}

func unsupportedSourceSymlinkError(fallbackName string, err error) error {
	var symlinkErr *symlink.Error
	if errors.As(err, &symlinkErr) {
		name := symlinkErr.Path
		if name == "" {
			name = fallbackName
		}
		return &output.InstallError{Err: fmt.Errorf(
			"addon source contains an unsupported symlink: %s (%s)", name, symlinkErr.Reason)}
	}
	return &output.InstallError{Err: fmt.Errorf(
		"addon source contains an unsupported symlink: %s (%v)", fallbackName, err)}
}

func wrapInstallError(err error) error {
	var installErr *output.InstallError
	if errors.As(err, &installErr) {
		return err
	}
	return &output.InstallError{Err: err}
}

func cleanExcludeDir(excludeDir string) string {
	return filepath.Clean(filepath.FromSlash(strings.ReplaceAll(excludeDir, `\`, `/`)))
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
