package installer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/CafecitoGames/godot-addon-manager/internal/source"
)

// Install resolves the source subtree within fetched.Dir and copies it into
// <addonsDir>/<spec.InstallName()>, replacing any existing directory.
func Install(fetched source.FetchResult, spec manifest.AddonSpec, addonsDir string) error {
	sourceRoot, err := resolveSourcePath(fetched.Dir, spec.SourcePath)
	if err != nil {
		return err
	}
	destination := filepath.Join(addonsDir, spec.InstallName())
	if err := os.RemoveAll(destination); err != nil {
		return &output.InstallError{Err: err}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return &output.InstallError{Err: err}
	}
	if err := copyTree(sourceRoot, destination); err != nil {
		return &output.InstallError{Err: err}
	}
	return nil
}

// resolveSourcePath returns the directory within root to install. When
// sourcePath is set it is used directly. Otherwise a single addons/<name>/
// directory is auto-detected; if no addons/ dir exists, root is used.
func resolveSourcePath(root, sourcePath string) (string, error) {
	if sourcePath != "" {
		resolved := filepath.Join(root, sourcePath)
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return "", &output.InstallError{Err: fmt.Errorf("source_path %q not found in fetched source", sourcePath)}
		}
		return resolved, nil
	}
	addonsDir := filepath.Join(root, "addons")
	entries, err := os.ReadDir(addonsDir)
	if err != nil {
		// No addons/ directory: install from the fetched root.
		return root, nil
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
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
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
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o200)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
