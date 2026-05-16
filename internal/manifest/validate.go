package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// validateAddonName rejects names that could escape the addons directory when
// used as a single path segment. It checks for empty strings, path separators,
// the relative-traversal components "." and "..", and absolute paths.
func validateAddonName(name string) error {
	if name == "" {
		return fmt.Errorf("addon name must not be empty")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("addon name %q must not be an absolute path", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("addon name %q must not contain path separators", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("addon name %q is not a valid directory name", name)
	}
	return nil
}

// validateSourcePath rejects source_path values that could escape the fetched
// source root. Absolute paths and any component equal to ".." are rejected.
func validateSourcePath(sourcePath string) error {
	if filepath.IsAbs(sourcePath) {
		return fmt.Errorf("source_path %q must not be an absolute path", sourcePath)
	}
	cleaned := filepath.Clean(sourcePath)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("source_path %q must not escape the source root", sourcePath)
	}
	return nil
}

// validateSpec checks one addon entry's required fields for its source type.
func validateSpec(name string, addon AddonSpec) error {
	if err := validateAddonName(name); err != nil {
		return err
	}
	if addon.InstallAs != "" {
		if err := validateAddonName(addon.InstallAs); err != nil {
			return fmt.Errorf("addon %q: invalid install_as: %w", name, err)
		}
	}
	if addon.SourcePath != "" {
		if err := validateSourcePath(addon.SourcePath); err != nil {
			return fmt.Errorf("addon %q: invalid source_path: %w", name, err)
		}
	}

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
