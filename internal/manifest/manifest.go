package manifest

import (
	"fmt"
	"os"

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
	for name, spec := range m.Addons {
		spec.Name = name
		m.Addons[name] = spec
	}
	return m, nil
}

// Save writes the manifest to path as TOML.
func (m *Manifest) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating manifest %s: %w", path, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(m); err != nil {
		return fmt.Errorf("encoding manifest %s: %w", path, err)
	}
	return nil
}
