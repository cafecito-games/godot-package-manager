package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// LockEntry pins one resolved addon for reproducible installs.
type LockEntry struct {
	ResolvedVersion string `toml:"resolved_version"`   // commit SHA or release tag
	SourcePath      string `toml:"source_path"`        // subtree actually installed
	Checksum        string `toml:"checksum,omitempty"` // SHA-256 for archive/release; empty for git
	SpecHash        string `toml:"spec_hash"`          // AddonSpec.Hash() it was resolved from

	// Slices maps every slice ID published by the addon's index to that
	// slice archive's SHA-256. It records the whole published set rather
	// than only the slices installed here, which is what keeps the lock
	// machine-independent: two contributors on different hosts write the
	// same table. An unsliced addon leaves this nil and uses Checksum.
	Slices map[string]string `toml:"slices,omitempty"`

	// Artifacts pins every non-selectable shared archive a format-2 index
	// publishes. Artifact IDs are generic platform names, so reconciliation can
	// derive a selected architecture slice's dependency without fetching the
	// already-pinned index.
	Artifacts map[string]string `toml:"artifacts,omitempty"`

	// IndexChecksum is the SHA-256 of the raw gpm-index.toml bytes the
	// Slices table was read from, so a retagged release cannot silently
	// repoint slice archives. It is set exactly when Slices is set.
	IndexChecksum string `toml:"index_sha256,omitempty"`
}

// Lockfile is the parsed contents of addons.lock. It is shared, committed, and
// machine-independent: it is the single authority for what an install must
// verify. What a particular machine has actually materialized on disk is a
// separate question, answered by State.
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
	if err := lockfile.Validate(); err != nil {
		return nil, err
	}
	return lockfile, nil
}

// Validate checks every lock entry for internally consistent slice pins. It
// returns an *output.ManifestError describing the first problem found, so a
// malformed lockfile fails at load rather than part-way through an install.
//
// Slice ID validity is delegated entirely to internal/slice; this package
// re-declares no part of the platform vocabulary. The pre-existing
// ResolvedVersion, SourcePath, and Checksum fields are deliberately left
// unchecked: lockfiles written by earlier versions must keep loading unchanged.
func (lockfile *Lockfile) Validate() error {
	for name, entry := range lockfile.Addons {
		if err := validateLockEntry(name, entry); err != nil {
			return &output.ManifestError{Err: err}
		}
	}
	return nil
}

// validateLockEntry checks one lock entry. An addon is either sliced (slices
// plus index_sha256) or unsliced (checksum), never both.
func validateLockEntry(name string, entry LockEntry) error {
	if entry.Slices == nil && entry.Artifacts == nil && entry.IndexChecksum == "" {
		return nil
	}
	if entry.Checksum != "" {
		return fmt.Errorf("addon %q: must not set both checksum and slices; an addon is either sliced or it is not", name)
	}
	if entry.IndexChecksum == "" {
		return fmt.Errorf("addon %q: slices requires index_sha256; a sliced entry without its index pin cannot be verified", name)
	}
	if entry.Slices == nil {
		return fmt.Errorf("addon %q: index_sha256 requires a slices table; an addon is either sliced or it is not", name)
	}
	if err := validateChecksum(entry.IndexChecksum); err != nil {
		return fmt.Errorf("addon %q: invalid index_sha256: %w", name, err)
	}
	if _, ok := entry.Slices[slice.CorePlatform]; !ok {
		return fmt.Errorf("addon %q: slices must record the %q slice", name, slice.CorePlatform)
	}
	for sliceID, checksum := range entry.Slices {
		if _, err := slice.ParseSliceID(sliceID); err != nil {
			return fmt.Errorf("addon %q: invalid slices key: %w", name, err)
		}
		if err := validateChecksum(checksum); err != nil {
			return fmt.Errorf("addon %q: invalid checksum for slice %q: %w", name, sliceID, err)
		}
	}
	for artifactID, checksum := range entry.Artifacts {
		id, err := slice.ParseSliceID(artifactID)
		if err != nil || id.IsCore() || id.Architecture != "" {
			return fmt.Errorf("addon %q: invalid artifacts key %q: must be one generic platform name", name, artifactID)
		}
		if err := validateChecksum(checksum); err != nil {
			return fmt.Errorf("addon %q: invalid checksum for artifact %q: %w", name, artifactID, err)
		}
		architectureCount := 0
		for sliceID := range entry.Slices {
			published, parseErr := slice.ParseSliceID(sliceID)
			if parseErr == nil && published.Platform == artifactID && published.Architecture != "" {
				architectureCount++
			}
		}
		if architectureCount < 2 {
			return fmt.Errorf("addon %q: artifact %q has %d architecture slices; a shared artifact requires at least two", name, artifactID, architectureCount)
		}
	}
	return nil
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
