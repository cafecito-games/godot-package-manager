package cli

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/cafecito-games/godot-package-manager/internal/installer"
	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
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
}

// Runner performs install orchestration. FetcherFor is injectable for testing;
// production code sets it to source.FetcherFor via NewRunner.
type Runner struct {
	AddonsDir  string
	LockPath   string
	FetcherFor func(manifest.AddonSpec) (source.Fetcher, error)
}

// NewRunner builds a Runner wired to the real source layer using the given
// size limits. A zero source.Limits value yields the built-in defaults.
func NewRunner(addonsDir, lockPath string, limits source.Limits) *Runner {
	return &Runner{AddonsDir: addonsDir, LockPath: lockPath, FetcherFor: source.FetcherForWithLimits(limits)}
}

// InstallAddons fetches and installs the named addons (all addons when names is
// nil/empty), then writes addons.lock. It returns one AddonResult per addon.
// When mode is ModeInstall, existing lock entries that are consistent with the
// manifest are honored for reproducible installs. When mode is ModeUpdate,
// every target is re-resolved regardless of lock state.
func (r *Runner) InstallAddons(ctx context.Context, addonManifest *manifest.Manifest, names []string, mode InstallMode) ([]AddonResult, error) {
	lock, err := manifest.LoadLock(r.LockPath)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if _, ok := addonManifest.Addons[name]; !ok {
			return nil, &output.ManifestError{Err: fmt.Errorf("unknown addon %q", name)}
		}
	}
	targets := selectAddons(addonManifest, names)
	var results []AddonResult
	for _, spec := range targets {
		useLock := mode == ModeInstall && !manifest.NeedsResolve(spec, lock)

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

		if err := verifyChecksum(spec, lock, fetched, useLock); err != nil {
			_ = os.RemoveAll(fetched.Dir)
			return nil, err
		}

		err = installer.Install(fetched, spec, r.AddonsDir)
		_ = os.RemoveAll(fetched.Dir)
		if err != nil {
			return nil, err
		}
		lock.Addons[spec.Name] = manifest.LockEntry{
			ResolvedVersion: fetched.ResolvedVersion,
			SourcePath:      spec.SourcePath,
			Checksum:        fetched.Checksum,
			SpecHash:        spec.Hash(),
		}
		if err := lock.Save(r.LockPath); err != nil {
			return nil, err
		}
		results = append(results, AddonResult{
			Name:            spec.Name,
			ResolvedVersion: fetched.ResolvedVersion,
			InstallPath:     spec.InstallName(),
		})
	}
	// On a full run, drop lock entries for addons no longer in the manifest so
	// addons.lock does not accumulate stale pins.
	if len(names) == 0 {
		for name := range lock.Addons {
			if _, ok := addonManifest.Addons[name]; !ok {
				delete(lock.Addons, name)
			}
		}
	}
	if err := lock.Save(r.LockPath); err != nil {
		return nil, err
	}
	return results, nil
}

// verifyChecksum checks a fetched archive against the manifest-declared
// checksum (whenever one is set) and, when installing from an existing pin,
// against the lockfile checksum. Git sources report no checksum and are
// skipped.
func verifyChecksum(spec manifest.AddonSpec, lock *manifest.Lockfile, fetched source.FetchResult, useLock bool) error {
	if spec.Checksum != "" && fetched.Checksum != "" && spec.Checksum != fetched.Checksum {
		return &output.FetchError{Err: fmt.Errorf(
			"addon %q: checksum mismatch (manifest: %s, fetched: %s)",
			spec.Name, spec.Checksum, fetched.Checksum)}
	}
	if useLock {
		entry := lock.Addons[spec.Name]
		if entry.Checksum != "" && fetched.Checksum != "" && entry.Checksum != fetched.Checksum {
			return &output.FetchError{Err: fmt.Errorf(
				"addon %q: checksum mismatch (lock: %s, fetched: %s)",
				spec.Name, entry.Checksum, fetched.Checksum)}
		}
	}
	return nil
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
