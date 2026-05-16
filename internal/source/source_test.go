package source

import (
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/stretchr/testify/require"
)

func TestFetcherForKnownTypes(t *testing.T) {
	for _, sourceType := range []manifest.SourceType{
		manifest.SourceGit, manifest.SourceArchive, manifest.SourceGitHubRelease,
	} {
		fetcher, err := FetcherFor(manifest.AddonSpec{Source: sourceType})
		require.NoError(t, err)
		require.NotNil(t, fetcher)
	}
}

func TestFetcherForUnknownType(t *testing.T) {
	_, err := FetcherFor(manifest.AddonSpec{Source: "ftp"})
	require.Error(t, err)
}
