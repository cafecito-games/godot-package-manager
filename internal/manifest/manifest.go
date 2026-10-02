package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// projectTableKey is the name of the one table decoded strictly.
const projectTableKey = "project"

// Load reads and parses addons.toml at path. Each AddonSpec.Name is set from
// its TOML table key, and each AddonSpec.Platforms is resolved to the addon's
// own declared list when it has one and to the project list otherwise. This is
// the only place the effective platform list is computed; every consumer reads
// AddonSpec.Platforms.
//
// Unknown keys are rejected under [project] and ignored everywhere else. That
// asymmetry is deliberate: [project] is new, so strictness there cannot reject
// a manifest that installs today, while a manifest carrying a stray key under
// [addons.<name>] keeps loading exactly as it did before.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest %s: %w", path, err)
	}
	m := &Manifest{}
	metaData, err := toml.Decode(string(data), m)
	if err != nil {
		return nil, fmt.Errorf("parsing manifest %s: %w", path, err)
	}
	if err := rejectUnknownProjectKeys(path, metaData); err != nil {
		return nil, err
	}
	if m.Addons == nil {
		m.Addons = map[string]AddonSpec{}
	}
	for name, addon := range m.Addons {
		addon.Name = name
		if addon.Platforms == nil {
			// A clone, not the project slice itself, so no later caller can
			// reach the project list through one addon.
			addon.Platforms = slices.Clone(m.Project.Platforms)
		}
		m.Addons[name] = addon
	}
	return m, nil
}

// rejectUnknownProjectKeys reports keys the decoder did not consume that live
// under [project]. MetaData.Undecoded is file-wide, so the keys are filtered to
// that table before being reported.
func rejectUnknownProjectKeys(path string, metaData toml.MetaData) error {
	var unknown []string
	for _, key := range metaData.Undecoded() {
		if len(key) > 1 && key[0] == projectTableKey {
			unknown = append(unknown, key.String())
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	return &output.ManifestError{Err: fmt.Errorf(
		"parsing manifest %s: unknown key(s) in [%s]: %s",
		path, projectTableKey, strings.Join(unknown, ", "),
	)}
}

// Save writes the manifest to path as TOML using an atomic rename so a
// mid-write failure never leaves a corrupted file at path.
func (m *Manifest) Save(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".addons-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp manifest: %w", err)
	}
	tmpName := tmp.Name()

	if err := toml.NewEncoder(tmp).Encode(m); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("encoding manifest %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("syncing manifest %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing manifest %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("installing manifest %s: %w", path, err)
	}
	return nil
}
