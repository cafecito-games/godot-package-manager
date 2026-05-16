package manifest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Load reads and parses addons.toml at path. Each AddonSpec.Name is set from
// its TOML table key.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest %s: %w", path, err)
	}
	m := &Manifest{}
	if err := toml.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("parsing manifest %s: %w", path, err)
	}
	if m.Addons == nil {
		m.Addons = map[string]AddonSpec{}
	}
	for name, addon := range m.Addons {
		addon.Name = name
		m.Addons[name] = addon
	}
	return m, nil
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
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("encoding manifest %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("syncing manifest %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing manifest %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("installing manifest %s: %w", path, err)
	}
	return nil
}
