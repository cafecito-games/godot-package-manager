package project

import (
	"fmt"
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
		if _, statErr := os.Stat(filepath.Join(dir, "project.godot")); statErr == nil {
			return forRoot(dir), nil
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
