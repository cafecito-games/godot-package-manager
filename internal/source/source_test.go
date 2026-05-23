package source

import (
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestFetcherForKnownTypes(t *testing.T) {
	for _, sourceType := range []manifest.SourceType{
		manifest.SourceGit, manifest.SourceArchive, manifest.SourceGitHubRelease,
	} {
		fetcher, err := FetcherFor(manifest.AddonSpec{Source: sourceType})
		require.NoError(t, err)
		require.NotNil(t, fetcher)

		switch sourceType {
		case manifest.SourceGit:
			require.IsType(t, &GitFetcher{}, fetcher)
		case manifest.SourceArchive:
			require.IsType(t, &ArchiveFetcher{}, fetcher)
		case manifest.SourceGitHubRelease:
			require.IsType(t, &GitHubReleaseFetcher{}, fetcher)
		}
	}
}

func TestFetcherForUnknownType(t *testing.T) {
	_, err := FetcherFor(manifest.AddonSpec{Source: "ftp"})
	require.Error(t, err)
	var fetchError *output.FetchError
	require.ErrorAs(t, err, &fetchError)
}

func TestFetcherForWithLimitsPropagatesToArchive(t *testing.T) {
	limits := Limits{MaxDownloadBytes: 7, MaxExtractedBytes: 11}
	fetcher, err := FetcherForWithLimits(limits)(manifest.AddonSpec{Source: manifest.SourceArchive})
	require.NoError(t, err)
	archive, ok := fetcher.(*ArchiveFetcher)
	require.True(t, ok)
	require.Equal(t, int64(7), archive.maxBytes)
	require.Equal(t, int64(11), archive.maxExtracted)
}

func TestFetcherForWithLimitsPropagatesToGitHubRelease(t *testing.T) {
	limits := Limits{MaxDownloadBytes: 13, MaxExtractedBytes: 17}
	fetcher, err := FetcherForWithLimits(limits)(manifest.AddonSpec{Source: manifest.SourceGitHubRelease})
	require.NoError(t, err)
	ghRelease, ok := fetcher.(*GitHubReleaseFetcher)
	require.True(t, ok)
	require.Equal(t, int64(13), ghRelease.maxBytes)
	require.Equal(t, int64(17), ghRelease.maxExtracted)
}

func TestFetcherForWithZeroLimitsLeavesDefaults(t *testing.T) {
	fetcher, err := FetcherForWithLimits(Limits{})(manifest.AddonSpec{Source: manifest.SourceArchive})
	require.NoError(t, err)
	archive, ok := fetcher.(*ArchiveFetcher)
	require.True(t, ok)
	require.Zero(t, archive.maxBytes)
	require.Zero(t, archive.maxExtracted)
}
