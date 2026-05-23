package source

import (
	"context"
	"fmt"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
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

// Limits overrides the default download and extraction size caps. A zero value
// for any field falls back to the package default.
type Limits struct {
	MaxDownloadBytes  int64
	MaxExtractedBytes int64
}

// FetcherFor returns the Fetcher matching the spec's source type using the
// package's default size limits.
func FetcherFor(spec manifest.AddonSpec) (Fetcher, error) {
	return FetcherForWithLimits(Limits{})(spec)
}

// FetcherForWithLimits returns a factory that produces fetchers configured with
// the given size limits. A zero Limits value matches the behavior of FetcherFor.
func FetcherForWithLimits(limits Limits) func(manifest.AddonSpec) (Fetcher, error) {
	return func(spec manifest.AddonSpec) (Fetcher, error) {
		switch spec.Source {
		case manifest.SourceGit:
			return &GitFetcher{}, nil
		case manifest.SourceArchive:
			return &ArchiveFetcher{maxBytes: limits.MaxDownloadBytes, maxExtracted: limits.MaxExtractedBytes}, nil
		case manifest.SourceGitHubRelease:
			return &GitHubReleaseFetcher{maxBytes: limits.MaxDownloadBytes, maxExtracted: limits.MaxExtractedBytes}, nil
		default:
			return nil, &output.FetchError{Err: fmt.Errorf("no fetcher for source %q", spec.Source)}
		}
	}
}
