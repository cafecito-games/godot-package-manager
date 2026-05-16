package source

import (
	"context"
	"fmt"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
)

// FetchResult is the outcome of fetching an addon source into a temp directory.
type FetchResult struct {
	Dir             string // local path to the fetched tree
	ResolvedVersion string // commit SHA (git) or release tag actually obtained
	Checksum        string // SHA-256 of the archive/asset; empty for git sources
}

// Fetcher retrieves an addon source into a local temporary directory.
// Callers are responsible for removing FetchResult.Dir when done.
type Fetcher interface {
	Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error)
}

// FetcherFor returns the Fetcher matching the spec's source type.
func FetcherFor(spec manifest.AddonSpec) (Fetcher, error) {
	switch spec.Source {
	case manifest.SourceGit:
		return &GitFetcher{}, nil
	case manifest.SourceArchive:
		return &ArchiveFetcher{}, nil
	case manifest.SourceGitHubRelease:
		return &GitHubReleaseFetcher{}, nil
	default:
		return nil, &output.FetchError{Err: fmt.Errorf("no fetcher for source %q", spec.Source)}
	}
}

