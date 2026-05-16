package manifest

import (
	"errors"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestValidateRejectsBadSource(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{"x": {Name: "x", Source: "ftp"}}}
	err := m.Validate()
	require.Error(t, err)
	var me *output.ManifestError
	require.True(t, errors.As(err, &me))
}

func TestValidateRejectsMissingFields(t *testing.T) {
	cases := []struct {
		name  string
		addon AddonSpec
	}{
		{
			name:  "git missing version",
			addon: AddonSpec{Name: "x", Source: SourceGit, URL: "u"},
		},
		{
			name:  "git missing url",
			addon: AddonSpec{Name: "x", Source: SourceGit, Version: "v1"},
		},
		{
			name:  "github-release missing repo",
			addon: AddonSpec{Name: "x", Source: SourceGitHubRelease, Version: "1.0"},
		},
		{
			name:  "github-release missing version",
			addon: AddonSpec{Name: "x", Source: SourceGitHubRelease, Repo: "o/r"},
		},
		{
			name:  "archive missing url",
			addon: AddonSpec{Name: "x", Source: SourceArchive},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Addons: map[string]AddonSpec{"x": tc.addon}}
			require.Error(t, m.Validate())
		})
	}
}

func TestValidateAcceptsValidManifest(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"g": {Name: "g", Source: SourceGit, URL: "u", Version: "v1"},
		"r": {Name: "r", Source: SourceGitHubRelease, Repo: "o/r", Version: "1.0"},
		"a": {Name: "a", Source: SourceArchive, URL: "u"},
	}}
	require.NoError(t, m.Validate())
}
