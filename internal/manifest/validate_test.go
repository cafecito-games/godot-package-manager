package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/output"
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
		"g": {Name: "g", Source: SourceGit, URL: "https://github.com/o/r.git", Version: "v1"},
		"r": {Name: "r", Source: SourceGitHubRelease, Repo: "o/r", Version: "1.0"},
		"a": {Name: "a", Source: SourceArchive, URL: "https://example.com/a.zip"},
	}}
	require.NoError(t, m.Validate())
}

func TestValidateRejectsDotDotAddonName(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"..": {Name: "..", Source: SourceGit, URL: "u", Version: "v1"},
	}}
	err := m.Validate()
	require.Error(t, err)
	var me *output.ManifestError
	require.True(t, errors.As(err, &me))
}

func TestValidateRejectsDotDotInstallAs(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"dialogue_manager": {
			Name:      "dialogue_manager",
			Source:    SourceGit,
			URL:       "u",
			Version:   "v1",
			InstallAs: "..",
		},
	}}
	err := m.Validate()
	require.Error(t, err)
	var me *output.ManifestError
	require.True(t, errors.As(err, &me))
}

func TestValidateRejectsInstallAsWithSeparator(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"dialogue_manager": {
			Name:      "dialogue_manager",
			Source:    SourceGit,
			URL:       "u",
			Version:   "v1",
			InstallAs: "foo/bar",
		},
	}}
	err := m.Validate()
	require.Error(t, err)
	var me *output.ManifestError
	require.True(t, errors.As(err, &me))
}

func TestValidateRejectsEscapingSourcePath(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"dialogue_manager": {
			Name:       "dialogue_manager",
			Source:     SourceGit,
			URL:        "u",
			Version:    "v1",
			SourcePath: "../escape",
		},
	}}
	err := m.Validate()
	require.Error(t, err)
	var me *output.ManifestError
	require.True(t, errors.As(err, &me))
}

func TestValidateRejectsEscapingExclude(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[addons]
[addons.dialogue_manager]
source = "archive"
url = "https://example.com/dialogue.zip"
exclude = ["../outside"]
`), 0o644))

	m, err := Load(path)
	require.NoError(t, err)

	err = m.Validate()
	require.Error(t, err)
	var me *output.ManifestError
	require.True(t, errors.As(err, &me))
	require.Contains(t, err.Error(), "exclude")
}

func TestValidateAcceptsNormalAddon(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"dialogue_manager": {
			Name:       "dialogue_manager",
			Source:     SourceGit,
			URL:        "https://github.com/owner/dialogue.git",
			Version:    "v1",
			InstallAs:  "dialogue_manager",
			SourcePath: "addons/dialogue_manager",
		},
	}}
	require.NoError(t, m.Validate())
}

func TestValidateRejectsDangerousGitURLs(t *testing.T) {
	cases := []string{
		"ext::sh -c 'touch /tmp/pwned'",
		"-oProxyCommand=evil",
		"transport::address",
	}
	for _, badURL := range cases {
		t.Run(badURL, func(t *testing.T) {
			m := &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceGit, URL: badURL, Version: "v1"},
			}}
			require.Error(t, m.Validate())
		})
	}
}

func TestValidateAcceptsSafeGitURLs(t *testing.T) {
	cases := []string{
		"https://github.com/owner/repo.git",
		"ssh://git@github.com/owner/repo.git",
		"git://example.com/repo.git",
		"git@github.com:owner/repo.git",
		"file:///srv/repos/addon.git",
		"/local/path/repo",
	}
	for _, goodURL := range cases {
		t.Run(goodURL, func(t *testing.T) {
			m := &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceGit, URL: goodURL, Version: "v1"},
			}}
			require.NoError(t, m.Validate())
		})
	}
}

func TestValidateRejectsLeadingDashGitVersion(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"x": {Name: "x", Source: SourceGit, URL: "https://github.com/o/r.git", Version: "--upload-pack=evil"},
	}}
	require.Error(t, m.Validate())
}

func TestValidateRejectsNonHTTPArchiveURL(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"x": {Name: "x", Source: SourceArchive, URL: "file:///etc/passwd"},
	}}
	require.Error(t, m.Validate())
}

func TestValidateChecksumField(t *testing.T) {
	validDigest := "0000000000000000000000000000000000000000000000000000000000000000"

	t.Run("valid digest accepted", func(t *testing.T) {
		m := &Manifest{Addons: map[string]AddonSpec{
			"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Checksum: validDigest},
		}}
		require.NoError(t, m.Validate())
	})
	t.Run("malformed digest rejected", func(t *testing.T) {
		m := &Manifest{Addons: map[string]AddonSpec{
			"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Checksum: "abc123"},
		}}
		require.Error(t, m.Validate())
	})
	t.Run("checksum on git source rejected", func(t *testing.T) {
		m := &Manifest{Addons: map[string]AddonSpec{
			"x": {Name: "x", Source: SourceGit, URL: "https://github.com/o/r.git", Version: "v1", Checksum: validDigest},
		}}
		require.Error(t, m.Validate())
	})
}

func TestValidatePlatformsAndIndexFailClosed(t *testing.T) {
	cases := []struct {
		name          string
		manifest      *Manifest
		wantError     bool
		errorContains string
	}{
		{
			name: "valid declared platforms accepted",
			manifest: &Manifest{
				Project: ProjectConfig{Platforms: []string{"ios.arm64", "macos"}},
				Addons: map[string]AddonSpec{
					"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Platforms: []string{"ios.arm64", "macos"}},
				},
			},
		},
		{
			name: "project platforms absent accepted",
			manifest: &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip"},
			}},
		},
		{
			name: "project platforms empty accepted",
			manifest: &Manifest{
				Project: ProjectConfig{Platforms: []string{}},
				Addons: map[string]AddonSpec{
					"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Platforms: []string{}},
				},
			},
		},
		{
			name: "duplicate platforms accepted",
			manifest: &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Platforms: []string{"macos", "macos"}},
			}},
		},
		{
			name:          "unknown project platform rejected",
			manifest:      &Manifest{Project: ProjectConfig{Platforms: []string{"solaris"}}},
			wantError:     true,
			errorContains: "unknown platform",
		},
		{
			name:          "core in project platforms rejected",
			manifest:      &Manifest{Project: ProjectConfig{Platforms: []string{"core"}}},
			wantError:     true,
			errorContains: "implicit",
		},
		{
			name: "core in per-addon platforms rejected",
			manifest: &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Platforms: []string{"core"}},
			}},
			wantError:     true,
			errorContains: "implicit",
		},
		{
			name: "unknown per-addon platform rejected",
			manifest: &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Platforms: []string{"macos.sparc"}},
			}},
			wantError:     true,
			errorContains: "architecture",
		},
		{
			name: "index on archive accepted",
			manifest: &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Index: "https://example.com/gpm-index.toml"},
			}},
		},
		{
			name: "index on git rejected",
			manifest: &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceGit, URL: "https://example.com/r.git", Version: "v1", Index: "https://example.com/gpm-index.toml"},
			}},
			wantError:     true,
			errorContains: "index",
		},
		{
			name: "index on github-release rejected",
			manifest: &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceGitHubRelease, Repo: "o/r", Version: "1.0", Index: "https://example.com/gpm-index.toml"},
			}},
			wantError:     true,
			errorContains: "index",
		},
		{
			name: "relative index URL rejected",
			manifest: &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Index: "gpm-index.toml"},
			}},
			wantError:     true,
			errorContains: "scheme",
		},
		{
			name: "non-http index URL rejected",
			manifest: &Manifest{Addons: map[string]AddonSpec{
				"x": {Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Index: "file:///etc/passwd"},
			}},
			wantError:     true,
			errorContains: "scheme",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.manifest.Validate()
			if !testCase.wantError {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var manifestError *output.ManifestError
			require.True(t, errors.As(err, &manifestError))
			require.Equal(t, output.ExitCode(3), output.CodeFor(err))
			require.Contains(t, err.Error(), testCase.errorContains)
		})
	}
}
