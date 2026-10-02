// Package packager is the producer half of the addon platform slices feature.
// It reads an addon author's gpm-package.toml, partitions the addon subtree into
// a core slice plus one slice per platform, writes one reproducible zip archive
// per slice, and emits the gpm-index.toml that describes them.
//
// It orchestrates only. The platform vocabulary, slice identity, the index
// schema, and .gdextension partitioning all belong to internal/slice, and addon
// name validity belongs to internal/manifest; this package re-declares none of
// them. It performs no network access.
package packager

import (
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// ConfigFileName is the author-committed config `gpm package` reads from the
// addon repository.
const ConfigFileName = "gpm-package.toml"

// extensionSuffix is the file extension of a Godot extension descriptor. A file
// with this suffix is partitioned rather than archived as it is on disk.
const extensionSuffix = ".gdextension"

// Config is a parsed gpm-package.toml. It is the only declaration of that
// schema.
type Config struct {
	Package PackageSection `toml:"package"`
}

// PackageSection is the [package] table: which subtree of the author's
// repository is the addon, what it is called, what version is being cut, and any
// extra files no .gdextension key references.
type PackageSection struct {
	Name      string `toml:"name"`
	AddonPath string `toml:"addon_path"`
	Version   string `toml:"version"`

	// Slices maps a slice ID in its canonical tag form to the globs naming extra
	// files that belong to that slice. Android plugins ship .aar files and iOS
	// ships .xcframework bundles that no [libraries] key references, so extras
	// are required in practice rather than a convenience.
	Slices map[string][]string `toml:"slices"`
}

// LoadConfig reads and validates gpm-package.toml at path.
//
// Decoding is strict in both directions: an unknown key anywhere in the document
// is rejected, matching what LoadIndex does for gpm-index.toml rather than the
// asymmetric decoding manifest.Load uses. A key gpm ignores today could carry
// meaning in a future gpm, and silently dropping it would publish archives that
// do not match what the author asked for.
//
// Only the rules that need no filesystem access are checked here: everything
// about the tree on disk is checked while packaging, where the repository root
// is known. Every failure is an *output.ManifestError, because gpm-package.toml
// is the author's own file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, manifestErrorf("no %s at %s; `gpm package` runs in an addon repository and reads its %s", ConfigFileName, path, ConfigFileName)
		}
		return nil, manifestErrorf("reading %s: %s", path, err)
	}
	config := &Config{}
	metaData, err := toml.Decode(string(data), config)
	if err != nil {
		return nil, manifestErrorf("parsing %s: %s", path, err)
	}
	if err := rejectUnknownConfigKeys(path, metaData); err != nil {
		return nil, err
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return config, nil
}

// topLevelConfigKeys is the exact spelling of every key the config declares at
// the top level.
var topLevelConfigKeys = []string{"package"}

// packageFieldNames is the exact spelling of every key the [package] table
// declares.
var packageFieldNames = []string{"name", "addon_path", "version", "slices"}

// rejectUnknownConfigKeys fails on any key the schema does not declare. The
// smallest undecoded key is reported, so a config with several unknown keys
// always produces the same message.
func rejectUnknownConfigKeys(path string, metaData toml.MetaData) error {
	// The TOML library matches a struct field case-insensitively when no exact
	// match exists, and records the document's own spelling as decoded. TOML keys
	// are case-sensitive, so a key differing only in case is an unknown key that
	// strict decoding alone lets through, which is why the index loader checks
	// the document's spellings as well as its undecoded keys. This config is
	// decoded strictly in both directions for the same reason.
	for _, key := range metaData.Keys() {
		if err := rejectMisspelledConfigKey(path, key); err != nil {
			return err
		}
	}

	undecoded := metaData.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	smallest := undecoded[0].String()
	for _, key := range undecoded[1:] {
		if key.String() < smallest {
			smallest = key.String()
		}
	}
	return manifestErrorf("%s declares unknown key %q", path, smallest)
}

// rejectMisspelledConfigKey checks one document key against the schema at the
// position it appears in. The one position the schema leaves open is a
// [package.slices] key, which is a slice ID validated later as a value.
func rejectMisspelledConfigKey(path string, key toml.Key) error {
	switch {
	case len(key) == 0:
		return nil
	case !slices.Contains(topLevelConfigKeys, key[0]):
		return manifestErrorf("%s declares unknown key %q", path, key.String())
	case len(key) == 1:
		return nil
	case key[1] == "slices":
		// Below [package.slices] every key is a slice ID, and only its own table
		// of patterns sits under it, which carries no keys at all.
		if len(key) > 3 {
			return manifestErrorf("%s declares unknown key %q", path, key.String())
		}
		return nil
	case !slices.Contains(packageFieldNames, key[1]):
		return manifestErrorf("%s declares unknown key %q", path, key.String())
	case len(key) > 2:
		return manifestErrorf("%s declares unknown key %q", path, key.String())
	}
	return nil
}

// validate applies every rule that needs nothing from disk.
func (config *Config) validate() error {
	if err := manifest.ValidateAddonName(config.Package.Name); err != nil {
		return manifestErrorf("invalid [package] name: %s", err)
	}
	if err := validateAddonPath(config.Package.AddonPath); err != nil {
		return manifestErrorf("invalid [package] addon_path: %s", err)
	}
	for _, id := range sortedConfigSliceIDs(config.Package.Slices) {
		// ParseDeclaredPlatform rather than ParseSliceID: "core" is the slice
		// everything unclaimed falls into, so naming it as an extras target is
		// either a misunderstanding or a no-op, and both are worth reporting.
		//
		// Reported by message rather than wrapped: the inner error is already an
		// *output.ManifestError, and wrapping it would nest one exit-code carrier
		// inside another for no gain.
		if _, err := slice.ParseDeclaredPlatform(id); err != nil {
			return manifestErrorf("invalid [package.slices] key %q: %s", id, err)
		}
		if len(config.Package.Slices[id]) == 0 {
			return manifestErrorf(
				"[package.slices] %q names no path; a slice with no extras is omitted rather than declared empty", id,
			)
		}
		for _, pattern := range config.Package.Slices[id] {
			if err := validateExtraPattern(pattern); err != nil {
				return manifestErrorf("invalid [package.slices] %q pattern: %s", id, err)
			}
		}
	}
	return nil
}

// validateAddonPath rejects an addon_path that is not a clean relative
// subdirectory of the repository. The path is joined to the repository root and
// every file under it is archived, so an absolute path or a traversal would
// publish files from outside the repository.
func validateAddonPath(addonPath string) error {
	if addonPath == "" {
		return fmt.Errorf("addon_path is required; it names the subtree that becomes the addon")
	}
	return validateRepositoryRelativePath(addonPath)
}

// validateExtraPattern rejects an extras glob that could reach outside
// addon_path. A pattern is matched against paths relative to addon_path, so the
// containment rule is the same one a path obeys, applied to the pattern before
// any file is matched: a pattern that could never be written as a clean relative
// path can never name a file inside the addon.
func validateExtraPattern(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("pattern must not be empty")
	}
	if err := validateRepositoryRelativePath(strings.ReplaceAll(pattern, "**", "_")); err != nil {
		return err
	}
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "**" {
			continue
		}
		if _, err := path.Match(segment, ""); err != nil {
			return fmt.Errorf("pattern %q has malformed segment %q: %s", pattern, segment, err)
		}
	}
	return nil
}

// validateRepositoryRelativePath rejects every shape that is not a clean
// relative POSIX path: an absolute path, a Windows path or separator, an empty
// or dotted component, and a traversal.
func validateRepositoryRelativePath(value string) error {
	if strings.Contains(value, `\`) {
		return fmt.Errorf("path %q must use %q separators", value, "/")
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, ":") {
		return fmt.Errorf("path %q must be relative to the repository root", value)
	}
	for _, component := range strings.Split(value, "/") {
		switch component {
		case "":
			return fmt.Errorf("path %q has an empty component", value)
		case ".":
			return fmt.Errorf("path %q is not in its simplest form", value)
		case "..":
			return fmt.Errorf("path %q must not escape the repository root", value)
		}
	}
	return nil
}

// sortedConfigSliceIDs returns the declared extras slice IDs in ascending order,
// so a config with several problems always reports the same one.
func sortedConfigSliceIDs(slices map[string][]string) []string {
	return sortedKeys(slices)
}

func manifestErrorf(format string, arguments ...any) error {
	return &output.ManifestError{Err: fmt.Errorf(format, arguments...)}
}

func installErrorf(format string, arguments ...any) error {
	return &output.InstallError{Err: fmt.Errorf(format, arguments...)}
}
