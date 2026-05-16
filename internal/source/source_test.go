package source

import (
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
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
