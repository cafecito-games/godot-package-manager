package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// Project describes a located Godot project and its gpm-managed paths.
type Project struct {
	Root         string // directory containing project.godot
	ManifestPath string // <Root>/addons.toml
	LockPath     string // <Root>/addons.lock
	AddonsDir    string // <Root>/addons
}

// Discover walks up from startDir until it finds a directory containing
// project.godot. It returns an *output.ManifestError if none is found.
func Discover(startDir string) (*Project, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return nil, &output.ManifestError{Err: err}
	}
	for {
		projectFile := filepath.Join(dir, "project.godot")
		info, statErr := os.Stat(projectFile)
		if statErr == nil {
			if !info.Mode().IsRegular() {
				return nil, &output.ManifestError{
					Err: fmt.Errorf("%s is not a regular file", projectFile),
				}
			}
			return forRoot(dir), nil
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			return nil, &output.ManifestError{Err: fmt.Errorf("checking %s: %w", projectFile, statErr)}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, &output.ManifestError{
				Err: fmt.Errorf("no project.godot found in %s or any parent directory", startDir),
			}
		}
		dir = parent
	}
}

// forRoot builds a Project for a known project root directory.
func forRoot(root string) *Project {
	return &Project{
		Root:         root,
		ManifestPath: filepath.Join(root, "addons.toml"),
		LockPath:     filepath.Join(root, "addons.lock"),
		AddonsDir:    filepath.Join(root, "addons"),
	}
}
