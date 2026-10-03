package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"
	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// CurrentFileManifestVersion records required regular-file paths without
// hashing contents. See StateEntry.FileManifestVersion for the exact contract.
const CurrentFileManifestVersion = 1

// StateEntry records what one addon materialized on this machine.
type StateEntry struct {
	ResolvedVersion string   `toml:"resolved_version"`    // the version these slices came from
	Slices          []string `toml:"slices"`              // slice IDs present in addons/ on this disk
	Artifacts       []string `toml:"artifacts,omitempty"` // shared artifact IDs present on this disk

	// FileManifestVersion selects the on-disk completeness check. Version 1
	// records the relative paths of installed regular files in Files and checks
	// that each path still names a regular file. This deliberately does not hash
	// contents or reject extra paths, so in-place edits and additions are left
	// alone; it also does not detect removal of an empty directory.
	FileManifestVersion int      `toml:"file_manifest_version,omitempty"`
	Files               []string `toml:"files,omitempty"`

	// Pin identifies the lockfile pin these slices were materialized from: a
	// sliced addon's index_sha256, or an unsliced addon's archive checksum.
	//
	// It is recorded because resolved_version does not identify content. A
	// release keeps its tag when it is republished, so two branches of one
	// repository can share an addons.toml — and therefore a spec_hash and a
	// resolved version — while their committed addons.lock files pin different
	// bytes. Without the pin, an entry written against one lock would satisfy
	// the other and leave the wrong files in place, unfetched and unverified.
	//
	// It is empty for a source whose resolved version is already its content
	// identity, which is every git source.
	Pin string `toml:"pin,omitempty"`
}

// State is the parsed contents of .gpm-state.toml, a machine-local and
// gitignored record of which slices this disk actually holds.
//
// State is never authoritative over the lockfile. It never answers "are these
// bytes correct" — addons.lock is the only verification authority — it answers
// which slice IDs this machine materialized and whether each recorded file path
// is still present. Discarding State can therefore cost extra work but can never
// produce an unverified install, which is what makes the fail-open in LoadState
// safe.
type State struct {
	Addons map[string]StateEntry `toml:"addons"`
}

// CorruptStateError reports that .gpm-state.toml could not be parsed. It is
// deliberately not an *output.ManifestError: a caller that forwards it must not
// be able to turn an unparseable local cache into exit code 3. The caller
// contract is to use the empty State that LoadState returns alongside it and to
// report this through diagnostics, never to abort the run.
type CorruptStateError struct {
	Path string
	Err  error
}

func (e *CorruptStateError) Error() string {
	return fmt.Sprintf("ignoring unparseable state file %s: %v", e.Path, e.Err)
}

func (e *CorruptStateError) Unwrap() error { return e.Err }

// LoadState reads .gpm-state.toml at path.
//
// A missing file yields an empty State and no error: every addon is then treated
// as having nothing on disk, so an install re-materializes it.
//
// An unparseable file also yields a non-nil empty State, together with a
// *CorruptStateError. This is the one place in gpm where malformed input is
// treated as absent, and it is deliberate: .gpm-state.toml is a machine-local
// cache whose entire content is recoverable from addons.lock plus the contents
// of addons/. Treating it as empty costs a re-download, whereas treating it as
// fatal would wedge an install behind a file the user is not meant to care
// about and cannot usefully repair. Because the error is returned rather than
// swallowed, the failure is still reportable.
//
// A file that exists but cannot be read is an environment fault rather than a
// cache miss, so it is returned as a plain error with no State.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Addons: map[string]StateEntry{}}, nil
	}
	if err != nil {
		return nil, &output.ManifestError{Err: fmt.Errorf("reading state file %s: %w", path, err)}
	}
	state := &State{}
	if err := toml.Unmarshal(data, state); err != nil {
		return &State{Addons: map[string]StateEntry{}}, &CorruptStateError{Path: path, Err: err}
	}
	if state.Addons == nil {
		state.Addons = map[string]StateEntry{}
	}
	return state, nil
}

// Save writes the state to path as TOML using an atomic rename, so a mid-write
// failure never leaves a corrupted or truncated file at path. Slice lists are
// sorted in the encoded output so repeated runs produce no diff; the receiver is
// left untouched.
func (state *State) Save(path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gpm-state-*.tmp")
	if err != nil {
		return &output.ManifestError{Err: fmt.Errorf("creating temp state file: %w", err)}
	}
	tmpName := tmp.Name()

	if err := toml.NewEncoder(tmp).Encode(state.sorted()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return &output.ManifestError{Err: fmt.Errorf("encoding state file %s: %w", path, err)}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return &output.ManifestError{Err: fmt.Errorf("syncing state file %s: %w", path, err)}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return &output.ManifestError{Err: fmt.Errorf("closing state file %s: %w", path, err)}
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return &output.ManifestError{Err: fmt.Errorf("installing state file %s: %w", path, err)}
	}
	return nil
}

// sorted returns a copy of the state with every slice and file list sorted, so
// that the encoded bytes depend only on the sets present and not on traversal or
// materialization order.
func (state *State) sorted() *State {
	normalized := &State{Addons: make(map[string]StateEntry, len(state.Addons))}
	for name, entry := range state.Addons {
		entry.Slices = slices.Sorted(slices.Values(entry.Slices))
		entry.Artifacts = slices.Sorted(slices.Values(entry.Artifacts))
		entry.Files = slices.Sorted(slices.Values(entry.Files))
		normalized.Addons[name] = entry
	}
	return normalized
}
