package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// Project describes a located Godot project and its gpm-managed paths.
type Project struct {
	Root         string // directory containing project.godot
	ManifestPath string // <Root>/addons.toml
	LockPath     string // <Root>/addons.lock
	AddonsDir    string // <Root>/addons
}

// ErrProjectNotFound marks discovery failure when no project.godot exists in
// the start directory or any parent directory.
var ErrProjectNotFound = errors.New("no project.godot found")

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
				Err: fmt.Errorf("%w in %s or any parent directory", ErrProjectNotFound, startDir),
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

var godotFeatureVersionPattern = regexp.MustCompile(`"([0-9]+\.[0-9]+(?:\.[0-9]+)?)"`)

// DetectGodotVersion reads project.godot and returns a best-effort major.minor
// version from config/features. It returns an empty string when no version-like
// feature is present or the project file cannot be read.
func DetectGodotVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "project.godot"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "config/features=") {
			continue
		}
		for _, match := range godotFeatureVersionPattern.FindAllStringSubmatch(line, -1) {
			if len(match) != 2 {
				continue
			}
			parts := strings.Split(match[1], ".")
			if len(parts) >= 2 {
				return parts[0] + "." + parts[1]
			}
		}
	}
	return ""
}
