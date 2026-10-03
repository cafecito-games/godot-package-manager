package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/installer"
	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
	"github.com/cafecito-games/godot-package-manager/internal/source"
)

// InstallMode controls whether the lockfile pins are honored.
type InstallMode int

const (
	// ModeInstall installs addons at their locked versions when the lock entry
	// is still consistent with addons.toml.
	ModeInstall InstallMode = iota
	// ModeUpdate ignores existing pins and re-resolves every target.
	ModeUpdate
)

// AddonResult reports the outcome of installing a single addon.
type AddonResult struct {
	Name            string `json:"name"`
	ResolvedVersion string `json:"resolved_version"`
	InstallPath     string `json:"install_path"`

	// Slices is what this machine has materialized for the addon, which is the
	// machine-readable form of the selection mode's effect. It is empty for an
	// unsliced addon.
	Slices []string `json:"slices,omitempty"`
}

// Runner performs install orchestration. FetcherFor is injectable for testing;
// production code sets it to source.FetcherFor via NewRunner.
type Runner struct {
	AddonsDir string
	LockPath  string

	// StatePath is the machine-local .gpm-state.toml. An empty value disables
	// the record entirely: nothing is read and nothing is written, so every
	// addon is re-materialized on every run. That is the only safe direction to
	// degrade in — extra work, never an unverified install.
	StatePath string

	// SelectionMode is how wide a slice set this run materializes. The zero
	// value is the declared-platforms default, so a Runner built without one
	// behaves as a default install does.
	SelectionMode slice.SelectionMode

	FetcherFor func(manifest.AddonSpec) (source.Fetcher, error)

	// Diagnosef reports a note about the run that is not a failure: a fetcher's
	// Diagnostics, or a state file that was ignored. The command layer sets it
	// to a verbosef closure, so the Runner writes to no stream of its own and a
	// nil value discards.
	Diagnosef func(format string, args ...any)
}

// NewRunner builds a Runner wired to the real source layer using the given
// size limits and slice selection mode. A zero source.Limits value yields the
// built-in defaults and a zero slice.SelectionMode the declared-platforms
// default.
func NewRunner(addonsDir, lockPath, statePath string, limits source.Limits, mode slice.SelectionMode) *Runner {
	return &Runner{
		AddonsDir:     addonsDir,
		LockPath:      lockPath,
		StatePath:     statePath,
		SelectionMode: mode,
		FetcherFor:    source.FetcherForWithLimits(limits, mode),
	}
}

func (r *Runner) diagnosef(format string, args ...any) {
	if r.Diagnosef == nil {
		return
	}
	r.Diagnosef(format, args...)
}

// InstallAddons fetches and installs the named addons (all addons when names is
// nil/empty), then writes addons.lock and .gpm-state.toml. It returns one
// AddonResult per addon.
//
// When mode is ModeInstall, an existing lock entry consistent with the manifest
// is honored for reproducible installs — but only once this machine is also
// confirmed to hold what it needs, because addons.lock is byte-identical across
// hosts and selection modes by design and therefore says nothing about this
// disk. When mode is ModeUpdate, every target is re-resolved regardless of lock
// or state.
func (r *Runner) InstallAddons(ctx context.Context, addonManifest *manifest.Manifest, names []string, mode InstallMode) ([]AddonResult, error) {
	lock, err := manifest.LoadLock(r.LockPath)
	if err != nil {
		return nil, err
	}
	state, err := r.loadState()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if _, ok := addonManifest.Addons[name]; !ok {
			return nil, &output.ManifestError{Err: fmt.Errorf("unknown addon %q", name)}
		}
	}
	targets := selectAddons(addonManifest, names)
	host := slice.CurrentHost()
	var results []AddonResult
	for _, spec := range targets {
		useLock := mode == ModeInstall && !manifest.NeedsResolve(spec, lock)

		if useLock {
			if result, satisfied := r.satisfiedOnDisk(spec, lock, state, host); satisfied {
				results = append(results, result)
				continue
			}
		}

		effectiveSpec := spec
		if useLock && spec.Source == manifest.SourceGit {
			// Pin the git fetch to the exact locked commit SHA so the shallow
			// clone checks out the same revision that was originally resolved.
			effectiveSpec.Version = lock.Addons[spec.Name].ResolvedVersion
		}

		fetcher, err := r.FetcherFor(effectiveSpec)
		if err != nil {
			return nil, err
		}
		fetched, err := fetcher.Fetch(ctx, effectiveSpec)
		if err != nil {
			return nil, err
		}
		// Reported here rather than by the fetcher: internal/source writes to no
		// stream, and a note about an unsupported host or an ignored manifest
		// field is useless if nothing ever renders it.
		for _, diagnostic := range fetched.Diagnostics {
			r.diagnosef("%s\n", diagnostic)
		}

		if err := verifyChecksum(spec, lock, fetched, useLock); err != nil {
			_ = os.RemoveAll(fetched.Dir)
			return nil, err
		}

		// The state record is dropped before the install rather than after it,
		// so that from here until both files are written this machine claims
		// nothing about the addon. Any failure in between — the install, the
		// lock write, the state write — therefore leaves a disk whose contents
		// no state entry vouches for, and the next run re-materializes it.
		// Writing the record afterwards alone would not do: an install that
		// succeeded and a lock write that then failed would leave new bytes on
		// disk under an old pin that the old state record still matched.
		if err := r.forgetState(state, spec.Name); err != nil {
			_ = os.RemoveAll(fetched.Dir)
			return nil, err
		}

		err = installer.Install(fetched, spec, r.AddonsDir)
		_ = os.RemoveAll(fetched.Dir)
		if err != nil {
			return nil, err
		}
		installedFiles, err := installedFileManifest(filepath.Join(r.AddonsDir, spec.InstallName()))
		if err != nil {
			return nil, &output.InstallError{Err: fmt.Errorf(
				"recording installed files for addon %q: %w", spec.Name, err)}
		}
		lockEntry := lockEntryFor(spec, fetched)
		lock.Addons[spec.Name] = lockEntry
		if err := lock.Save(r.LockPath); err != nil {
			return nil, err
		}
		// Recorded only once the install and the pin it was measured against
		// are both durable, so state never claims slices that are not on disk
		// or that the lock does not pin. The reverse — on disk but unrecorded —
		// only costs the next run a re-materialization.
		stateEntry := stateEntryFor(lockEntry, fetched, installedFiles)
		state.Addons[spec.Name] = stateEntry
		if err := r.saveState(state); err != nil {
			return nil, err
		}
		results = append(results, AddonResult{
			Name:            spec.Name,
			ResolvedVersion: fetched.ResolvedVersion,
			InstallPath:     spec.InstallName(),
			Slices:          slices.Clone(stateEntry.Slices),
		})
	}
	// On a full run, drop lock and state entries for addons no longer in the
	// manifest so neither file accumulates stale records.
	if len(names) == 0 {
		for name := range lock.Addons {
			if _, ok := addonManifest.Addons[name]; !ok {
				delete(lock.Addons, name)
			}
		}
		for name := range state.Addons {
			if _, ok := addonManifest.Addons[name]; !ok {
				delete(state.Addons, name)
			}
		}
	}
	if err := lock.Save(r.LockPath); err != nil {
		return nil, err
	}
	if err := r.saveState(state); err != nil {
		return nil, err
	}
	return results, nil
}

// loadState reads the machine-local state, reporting an unparseable file through
// diagnostics and continuing with the empty State that LoadState returns.
//
// An unparseable state file must not become exit 3: its entire content is
// recoverable by re-materializing, so treating it as empty costs a re-download,
// whereas failing would wedge an install behind a file the user is not meant to
// care about. A file that exists but cannot be read is a different thing — an
// environment fault rather than a cache miss — and LoadState reports it as an
// *output.ManifestError, which is returned unchanged.
func (r *Runner) loadState() (*manifest.State, error) {
	if r.StatePath == "" {
		return &manifest.State{Addons: map[string]manifest.StateEntry{}}, nil
	}
	state, err := manifest.LoadState(r.StatePath)
	var corrupt *manifest.CorruptStateError
	if errors.As(err, &corrupt) {
		r.diagnosef("%v\n", corrupt)
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	return state, nil
}

// forgetState drops the record for one addon and persists that immediately, so
// the window in which this machine is about to change what is on disk is a
// window in which it claims nothing about it.
func (r *Runner) forgetState(state *manifest.State, name string) error {
	if _, found := state.Addons[name]; !found {
		return nil
	}
	delete(state.Addons, name)
	return r.saveState(state)
}

func (r *Runner) saveState(state *manifest.State) error {
	if r.StatePath == "" {
		return nil
	}
	return state.Save(r.StatePath)
}

// satisfiedOnDisk reports whether this machine already holds exactly what spec
// needs, so a consistent lock entry can be honored without fetching anything.
//
// The needed set is recomputed from the current SelectionMode on every run and
// compared for equality rather than coverage, which is what makes a mode change
// self-healing in both directions: widening reports the new platforms as
// missing, and narrowing reports slices on disk that are no longer needed. Both
// re-fetch, and neither requires the user to delete anything.
func (r *Runner) satisfiedOnDisk(
	spec manifest.AddonSpec,
	lock *manifest.Lockfile,
	state *manifest.State,
	host slice.Host,
) (AddonResult, bool) {
	entry := lock.Addons[spec.Name]
	needed, err := neededSlices(spec, entry, host, r.SelectionMode)
	if err != nil {
		// The lock's published set is a cache of the index's. A selection that
		// cannot be computed from it is re-derived by fetching the index, which
		// is where the authoritative error comes from in any case.
		return AddonResult{}, false
	}
	recorded, trusted := recordedSlices(spec, lock, state, r.AddonsDir)
	if !trusted {
		return AddonResult{}, false
	}
	if problem, complete := installedFilesComplete(spec, state, r.AddonsDir); !complete {
		r.diagnosef("addon %q: %s; re-materializing\n", spec.Name, problem)
		return AddonResult{}, false
	}
	if len(missingSlicesOnDisk(spec, lock, state, needed, r.AddonsDir)) > 0 {
		return AddonResult{}, false
	}
	// The question is set equality rather than coverage, so narrowing the
	// selection re-materializes too: the installer replaces the addon directory
	// wholesale, which is what prunes the slices this mode no longer wants.
	if len(recorded) != len(needed) {
		return AddonResult{}, false
	}
	return AddonResult{
		Name:            spec.Name,
		ResolvedVersion: entry.ResolvedVersion,
		InstallPath:     spec.InstallName(),
		Slices:          slices.Clone(state.Addons[spec.Name].Slices),
	}, true
}

// neededSlices computes the slice IDs spec needs on host from the published set
// the lock records, under the given selection mode.
//
// An unsliced entry needs no slices at all: it publishes none. Its on-disk
// reconciliation is handled separately by the installed-file manifest.
func neededSlices(
	spec manifest.AddonSpec,
	entry manifest.LockEntry,
	host slice.Host,
	mode slice.SelectionMode,
) ([]slice.SliceID, error) {
	if entry.Slices == nil {
		return nil, nil
	}
	published := make([]slice.SliceID, 0, len(entry.Slices))
	for id := range entry.Slices {
		parsed, err := slice.ParseSliceID(id)
		if err != nil {
			return nil, err
		}
		published = append(published, parsed)
	}
	selection, err := slice.SelectSlices(spec.Platforms, host, published, mode)
	if err != nil {
		return nil, err
	}
	return selection.Slices, nil
}

// missingSlicesOnDisk returns the needed slice IDs this machine has not
// materialized for spec, sorted. It answers the machine-local question that
// NeedsResolve cannot: addons.lock is byte-identical across hosts and across
// selection modes by design, so a consistent lock says nothing about what is in
// addons/ on this disk.
func missingSlicesOnDisk(
	spec manifest.AddonSpec,
	lock *manifest.Lockfile,
	state *manifest.State,
	needed []slice.SliceID,
	addonsDir string,
) []slice.SliceID {
	recorded, trusted := recordedSlices(spec, lock, state, addonsDir)
	if !trusted {
		return slices.Clone(needed)
	}
	var missing []slice.SliceID
	for _, id := range needed {
		if _, found := recorded[id.String()]; !found {
			missing = append(missing, id)
		}
	}
	return missing
}

// recordedSlices returns the slice IDs this machine's state file records for
// spec, and whether that record may be trusted at all.
//
// It is the single definition of the conditions under which state records
// nothing usable: the install directory is absent, state has no entry for the
// addon, or the entry's resolved version or lock pin disagrees with the lock's.
// A state entry never makes an absent addon, nor one materialized from another
// version or another pin, look installed — which is also what makes an unsliced
// addon, which needs no slices and so has an empty missing set whatever the
// disk holds, reconcile correctly.
func recordedSlices(
	spec manifest.AddonSpec,
	lock *manifest.Lockfile,
	state *manifest.State,
	addonsDir string,
) (map[string]struct{}, bool) {
	info, err := os.Lstat(filepath.Join(addonsDir, spec.InstallName()))
	if err != nil || !info.IsDir() {
		return nil, false
	}
	entry, found := state.Addons[spec.Name]
	if !found {
		return nil, false
	}
	pinned := lock.Addons[spec.Name]
	if entry.ResolvedVersion != pinned.ResolvedVersion {
		return nil, false
	}
	// The version alone does not identify content: a republished release keeps
	// its tag, so a state entry written against one lock must not satisfy a
	// different one. Checking the pin is what keeps the fast path from leaving
	// another branch's files in place, unfetched and unverified.
	if entry.Pin != pinOf(pinned) {
		return nil, false
	}
	recorded := make(map[string]struct{}, len(entry.Slices))
	for _, id := range entry.Slices {
		recorded[id] = struct{}{}
	}
	return recorded, true
}

// lockEntryFor builds the lock pin for a completed fetch.
//
// A sliced fetch records the index digest and the whole published set rather
// than the slices this machine took, which is what keeps addons.lock identical
// on every host and in every selection mode; an unsliced one records the single
// archive checksum.
func lockEntryFor(spec manifest.AddonSpec, fetched source.FetchResult) manifest.LockEntry {
	entry := manifest.LockEntry{
		ResolvedVersion: fetched.ResolvedVersion,
		SourcePath:      spec.SourcePath,
		Checksum:        fetched.Checksum,
		SpecHash:        spec.Hash(),
	}
	if len(fetched.PublishedSlices) == 0 {
		return entry
	}
	entry.IndexChecksum = fetched.IndexChecksum
	entry.Slices = make(map[string]string, len(fetched.PublishedSlices))
	for _, published := range fetched.PublishedSlices {
		entry.Slices[published.ID.String()] = published.Checksum
	}
	return entry
}

// stateEntryFor records what the fetch actually materialized on this machine,
// which is the subset of the published set the selection mode asked for, and
// the lock pin it came from. The pin is read off the entry being written to the
// lock rather than recomputed, so the two records cannot disagree.
func stateEntryFor(entry manifest.LockEntry, fetched source.FetchResult, installedFiles []string) manifest.StateEntry {
	installed := make([]string, 0, len(fetched.InstalledSlices))
	for _, id := range fetched.InstalledSlices {
		installed = append(installed, id.String())
	}
	sort.Strings(installed)
	return manifest.StateEntry{
		ResolvedVersion:     fetched.ResolvedVersion,
		Slices:              installed,
		Pin:                 pinOf(entry),
		FileManifestVersion: manifest.CurrentFileManifestVersion,
		Files:               slices.Clone(installedFiles),
	}
}

// installedFileManifest returns the relative paths of the regular files under
// root. The installer rejects source symlinks, so encountering any non-regular
// leaf here means the installed tree cannot be described by the version 1 file
// manifest and the run fails without writing a state record.
func installedFileManifest(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("installed path %q is not a regular file", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// installedFilesComplete checks only the file paths recorded by the version 1
// state schema. It intentionally ignores file contents and additional paths:
// users commonly edit addons in place while developing them, and those edits
// must not turn every install into another fetch.
func installedFilesComplete(
	spec manifest.AddonSpec,
	state *manifest.State,
	addonsDir string,
) (string, bool) {
	entry := state.Addons[spec.Name]
	if entry.FileManifestVersion != manifest.CurrentFileManifestVersion {
		return "local state has no installed-file manifest", false
	}
	root := filepath.Join(addonsDir, spec.InstallName())
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() {
		return "install directory is missing or is not a directory", false
	}
	for _, recorded := range entry.Files {
		relative := filepath.Clean(filepath.FromSlash(recorded))
		if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return fmt.Sprintf("local state contains unsafe installed-file path %q", recorded), false
		}
		info, err := os.Lstat(filepath.Join(root, relative))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Sprintf("installed file %q is missing or is not a regular file", recorded), false
		}
	}
	return "", true
}

// pinOf reduces a lock entry to the digest identifying the content it pins: the
// index digest for a sliced addon, which in turn pins every slice archive, and
// the archive checksum for an unsliced one. It is empty for a source with no
// digest to carry, such as git, whose resolved commit SHA is already its
// content identity.
func pinOf(entry manifest.LockEntry) string {
	if entry.IndexChecksum != "" {
		return entry.IndexChecksum
	}
	return entry.Checksum
}

// verifyChecksum checks a fetched archive against the manifest-declared
// checksum (whenever one is set) and, when installing from an existing pin,
// against the lockfile checksum and the lockfile's slice pins. Git sources
// report no checksum and are skipped.
func verifyChecksum(spec manifest.AddonSpec, lock *manifest.Lockfile, fetched source.FetchResult, useLock bool) error {
	// An addon is either sliced or it is not, the same exclusivity the lockfile
	// validates. A result carrying both would be verified against one pin and
	// silently installed past the other.
	if fetched.Checksum != "" && len(fetched.PublishedSlices) > 0 {
		return &output.FetchError{Err: fmt.Errorf(
			"addon %q: the fetch reports both a single archive checksum and a published slice set; "+
				"an addon is either sliced or it is not", spec.Name)}
	}
	if spec.Checksum != "" && fetched.Checksum != "" && spec.Checksum != fetched.Checksum {
		return &output.FetchError{Err: fmt.Errorf(
			"addon %q: checksum mismatch (manifest: %s, fetched: %s)",
			spec.Name, spec.Checksum, fetched.Checksum)}
	}
	if !useLock {
		return nil
	}
	entry := lock.Addons[spec.Name]
	if entry.Checksum != "" && fetched.Checksum != "" && entry.Checksum != fetched.Checksum {
		return &output.FetchError{Err: fmt.Errorf(
			"addon %q: checksum mismatch (lock: %s, fetched: %s)",
			spec.Name, entry.Checksum, fetched.Checksum)}
	}
	return verifySlicePins(spec.Name, entry, fetched)
}

// verifySlicePins holds a sliced fetch against the lock's slice pins: the index
// digest, the published set, and every slice's checksum.
//
// A Fetcher never sees the lockfile, so this is the only place those pins are
// compared at all. Without it, a retagged release could serve a different
// gpm-index.toml naming different archives and the install would succeed with
// addons.toml and addons.lock both unchanged — self-consistent bytes that are
// not the pinned ones.
//
// The comparison is independent of the selection mode: the whole published set
// is verified even when only core and the host slice were materialized, because
// the pin is over what the index publishes rather than over what this disk took.
func verifySlicePins(name string, entry manifest.LockEntry, fetched source.FetchResult) error {
	if entry.Slices == nil && len(fetched.PublishedSlices) == 0 {
		return nil
	}
	if entry.Slices == nil {
		return &output.FetchError{Err: fmt.Errorf(
			"addon %q: the lock pins a single archive, but the fetch published a %s; "+
				"run gpm update to re-resolve it", name, slice.IndexFileName)}
	}
	if len(fetched.PublishedSlices) == 0 {
		return &output.FetchError{Err: fmt.Errorf(
			"addon %q: the lock pins a %s, but the fetch published no slices; "+
				"run gpm update to re-resolve it", name, slice.IndexFileName)}
	}
	if entry.IndexChecksum != fetched.IndexChecksum {
		return &output.FetchError{Err: fmt.Errorf(
			"addon %q: %s checksum mismatch (lock: %s, fetched: %s)",
			name, slice.IndexFileName, entry.IndexChecksum, fetched.IndexChecksum)}
	}
	fetchedChecksums := make(map[string]string, len(fetched.PublishedSlices))
	for _, published := range fetched.PublishedSlices {
		fetchedChecksums[published.ID.String()] = published.Checksum
	}
	if added := keysAbsentFrom(fetchedChecksums, entry.Slices); len(added) > 0 {
		return &output.FetchError{Err: fmt.Errorf(
			"addon %q: the fetch publishes slices the lock does not pin: %s", name, strings.Join(added, ", "))}
	}
	if removed := keysAbsentFrom(entry.Slices, fetchedChecksums); len(removed) > 0 {
		return &output.FetchError{Err: fmt.Errorf(
			"addon %q: the fetch no longer publishes slices the lock pins: %s", name, strings.Join(removed, ", "))}
	}
	for _, id := range slices.Sorted(maps.Keys(entry.Slices)) {
		if fetchedChecksums[id] != entry.Slices[id] {
			return &output.FetchError{Err: fmt.Errorf(
				"addon %q: slice %q checksum mismatch (lock: %s, fetched: %s)",
				name, id, entry.Slices[id], fetchedChecksums[id])}
		}
	}
	return nil
}

// keysAbsentFrom returns the keys of subject that other does not have, sorted so
// one difference reports one message.
func keysAbsentFrom[Value any](subject, other map[string]Value) []string {
	var absent []string
	for key := range subject {
		if _, found := other[key]; !found {
			absent = append(absent, key)
		}
	}
	sort.Strings(absent)
	return absent
}

// selectAddons returns the specs to operate on. An empty names slice selects
// all addons.
func selectAddons(addonManifest *manifest.Manifest, names []string) []manifest.AddonSpec {
	if len(names) == 0 {
		sortedNames := make([]string, 0, len(addonManifest.Addons))
		for name := range addonManifest.Addons {
			sortedNames = append(sortedNames, name)
		}
		sort.Strings(sortedNames)
		out := make([]manifest.AddonSpec, 0, len(sortedNames))
		for _, name := range sortedNames {
			out = append(out, addonManifest.Addons[name])
		}
		return out
	}
	var out []manifest.AddonSpec
	for _, name := range names {
		if spec, ok := addonManifest.Addons[name]; ok {
			out = append(out, spec)
		}
	}
	return out
}
