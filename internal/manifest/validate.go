package manifest

import (
	"fmt"

	"github.com/CafecitoGames/godot-addon-manager/internal/output"
)

// Validate checks every addon entry for required and consistent fields.
// It returns an *output.ManifestError describing the first problem found.
func (m *Manifest) Validate() error {
	for name, addon := range m.Addons {
		if err := validateSpec(name, addon); err != nil {
			return &output.ManifestError{Err: err}
		}
	}
	return nil
}

// validateSpec checks one addon entry's required fields for its source type.
func validateSpec(name string, addon AddonSpec) error {
	switch addon.Source {
	case SourceGit:
		if addon.URL == "" || addon.Version == "" {
			return fmt.Errorf("addon %q: git source requires url and version", name)
		}
	case SourceGitHubRelease:
		if addon.Repo == "" || addon.Version == "" {
			return fmt.Errorf("addon %q: github-release source requires repo and version", name)
		}
	case SourceArchive:
		if addon.URL == "" {
			return fmt.Errorf("addon %q: archive source requires url", name)
		}
	default:
		return fmt.Errorf("addon %q: unknown source %q (want git, github-release, or archive)", name, addon.Source)
	}
	return nil
}
