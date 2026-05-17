package cli

import (
	"os"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/project"
)

// loadProject discovers the Godot project from dir (or the current working
// directory) and loads its validated manifest.
func loadProject(dir string) (*project.Project, *manifest.Manifest, error) {
	if dir == "" {
		workingDir, err := os.Getwd()
		if err != nil {
			return nil, nil, err
		}
		dir = workingDir
	}
	discovered, err := project.Discover(dir)
	if err != nil {
		return nil, nil, err
	}
	addonManifest, err := manifest.Load(discovered.ManifestPath)
	if err != nil {
		return nil, nil, &output.ManifestError{Err: err}
	}
	if err := addonManifest.Validate(); err != nil {
		return nil, nil, err
	}
	return discovered, addonManifest, nil
}
