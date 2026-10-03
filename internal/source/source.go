package source

import (
	"context"
	"fmt"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// FetchResult is the outcome of fetching an addon source into a temp directory.
//
// The slice fields describe a sliced fetch, where the addon ships several
// archives described by a gpm-index.toml and only the ones this project needs
// were downloaded. They are all zero-valued for an unsliced fetch, which is
// every git source and every release or archive that publishes no index.
type FetchResult struct {
	Dir             string // local path to the fetched tree
	ResolvedVersion string // commit SHA (git) or release tag actually obtained
	Checksum        string // SHA-256 of the archive/asset; empty for git and sliced sources

	// IndexChecksum is the SHA-256 of the raw gpm-index.toml bytes, computed
	// before parsing so that a change altering only how the index parses cannot
	// evade the pin. Comparing it against the lockfile belongs to the caller:
	// a Fetcher never sees the lock.
	IndexChecksum string

	// PublishedSlices is every slice the index publishes, sorted by slice ID,
	// including the ones this fetch did not download. It is the whole published
	// set rather than the installed one, which is what keeps a lockfile written
	// from it machine-independent.
	PublishedSlices []SliceResult

	// PublishedArtifacts is the complete format-2 shared-artifact set, whether
	// or not this selection needed each artifact. Locks are built from this
	// machine-independent set.
	PublishedArtifacts []ArtifactResult

	// InstalledSlices is the subset actually downloaded, verified, and merged
	// into Dir, sorted by slice ID. It is what this machine has materialized,
	// as opposed to what the addon publishes.
	InstalledSlices []slice.SliceID

	// InstalledArtifacts is the automatic dependency closure materialized for
	// InstalledSlices, sorted by artifact ID.
	InstalledArtifacts []string

	// Diagnostics are notes about the fetch that are not failures: a host the
	// addon publishes no slice for, or a manifest field the sliced path ignores.
	// They travel on FetchResult because it is the only channel by which a fetch
	// outcome reaches the caller, and a Fetcher writes to no stream of its own.
	Diagnostics []string
}

// ArtifactResult records one non-selectable shared archive from a format-2
// index.
type ArtifactResult struct {
	ID       string
	Checksum string
	Size     int64
}

// Fetcher retrieves an addon source into a local temporary directory.
// Callers are responsible for removing FetchResult.Dir when done.
type Fetcher interface {
	Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error)
}

// Limits overrides the default download and extraction size caps. A zero value
// for any field falls back to the package default.
type Limits struct {
	MaxDownloadBytes  int64
	MaxExtractedBytes int64
}

// FetcherFor returns the Fetcher matching the spec's source type using the
// package's default size limits and the default slice selection mode.
func FetcherFor(spec manifest.AddonSpec) (Fetcher, error) {
	return FetcherForWithLimits(Limits{}, slice.SelectDeclaredPlatforms)(spec)
}

// FetcherForWithLimits returns a factory that produces fetchers configured with
// the given size limits and slice selection mode. A zero Limits value and the
// zero slice.SelectionMode together match the behavior of FetcherFor.
//
// The mode is only ever passed through to slice.SelectSlices on the sliced
// path: this package decides nothing about what a mode means, and reads no
// environment to discover one.
func FetcherForWithLimits(limits Limits, mode slice.SelectionMode) func(manifest.AddonSpec) (Fetcher, error) {
	return func(spec manifest.AddonSpec) (Fetcher, error) {
		switch spec.Source {
		case manifest.SourceGit:
			return &GitFetcher{}, nil
		case manifest.SourceArchive:
			return &ArchiveFetcher{
				maxBytes:      limits.MaxDownloadBytes,
				maxExtracted:  limits.MaxExtractedBytes,
				selectionMode: mode,
			}, nil
		case manifest.SourceGitHubRelease:
			return &GitHubReleaseFetcher{
				maxBytes:      limits.MaxDownloadBytes,
				maxExtracted:  limits.MaxExtractedBytes,
				selectionMode: mode,
			}, nil
		default:
			return nil, &output.FetchError{Err: fmt.Errorf("no fetcher for source %q", spec.Source)}
		}
	}
}
