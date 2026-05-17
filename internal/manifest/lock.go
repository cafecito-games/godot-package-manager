package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// LockEntry pins one resolved addon for reproducible installs.
type LockEntry struct {
	ResolvedVersion string `toml:"resolved_version"`   // commit SHA or release tag
	SourcePath      string `toml:"source_path"`        // subtree actually installed
	Checksum        string `toml:"checksum,omitempty"` // SHA-256 for archive/release; empty for git
	SpecHash        string `toml:"spec_hash"`          // AddonSpec.Hash() it was resolved from
}

// Lockfile is the parsed contents of addons.lock.
type Lockfile struct {
	Addons map[string]LockEntry `toml:"addons"`
}

// LoadLock reads addons.lock at path. A missing file yields an empty Lockfile
// and no error.
func LoadLock(path string) (*Lockfile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Lockfile{Addons: map[string]LockEntry{}}, nil
	}
	if err != nil {
		return nil, &output.ManifestError{Err: fmt.Errorf("reading lockfile %s: %w", path, err)}
	}
	lockfile := &Lockfile{}
	if err := toml.Unmarshal(data, lockfile); err != nil {
		return nil, &output.ManifestError{Err: fmt.Errorf("parsing lockfile %s: %w", path, err)}
	}
	if lockfile.Addons == nil {
		lockfile.Addons = map[string]LockEntry{}
	}
	return lockfile, nil
}

// Save writes the lockfile to path as TOML using an atomic rename so a
// mid-write failure never leaves a corrupted file at path.
func (lockfile *Lockfile) Save(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".addons-lock-*.tmp")
	if err != nil {
		return &output.ManifestError{Err: fmt.Errorf("creating temp lockfile: %w", err)}
	}
	tmpName := tmp.Name()

	if err := toml.NewEncoder(tmp).Encode(lockfile); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return &output.ManifestError{Err: fmt.Errorf("encoding lockfile %s: %w", path, err)}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return &output.ManifestError{Err: fmt.Errorf("syncing lockfile %s: %w", path, err)}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return &output.ManifestError{Err: fmt.Errorf("closing lockfile %s: %w", path, err)}
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return &output.ManifestError{Err: fmt.Errorf("installing lockfile %s: %w", path, err)}
	}
	return nil
}

// NeedsResolve reports whether spec must be re-fetched rather than installed
// from its existing lock pin.
func NeedsResolve(spec AddonSpec, lock *Lockfile) bool {
	entry, ok := lock.Addons[spec.Name]
	if !ok {
		return true
	}
	return entry.SpecHash != spec.Hash()
}
