package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/assetlib"
	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/source"
	"github.com/cafecito-games/godot-package-manager/internal/tui"
	"github.com/stretchr/testify/require"
)

type fakeAssetLibClient struct {
	configure func(context.Context, string) (assetlib.Configuration, error)
	search    func(context.Context, assetlib.SearchOptions) (assetlib.SearchResponse, error)
	get       func(context.Context, string) (assetlib.AssetDetail, error)
}

func (f fakeAssetLibClient) Configure(ctx context.Context, assetType string) (assetlib.Configuration, error) {
	if f.configure == nil {
		return assetlib.Configuration{}, errors.New("unexpected configure call")
	}
	return f.configure(ctx, assetType)
}

func (f fakeAssetLibClient) Search(ctx context.Context, opts assetlib.SearchOptions) (assetlib.SearchResponse, error) {
	if f.search == nil {
		return assetlib.SearchResponse{}, errors.New("unexpected search call")
	}
	return f.search(ctx, opts)
}

func (f fakeAssetLibClient) GetAsset(ctx context.Context, id string) (assetlib.AssetDetail, error) {
	if f.get == nil {
		return assetlib.AssetDetail{}, errors.New("unexpected get call")
	}
	return f.get(ctx, id)
}

func writeAssetLibProject(t *testing.T, features string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), []byte(features), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))
	return dir
}

func TestAssetLibSearchUsesDetectedGodotVersion(t *testing.T) {
	dir := writeAssetLibProject(t, `config/features=PackedStringArray("4.2", "Forward Plus")`)

	var got assetlib.SearchOptions
	testAssetLibClient = fakeAssetLibClient{
		search: func(_ context.Context, opts assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			got = opts
			return assetlib.SearchResponse{Results: []assetlib.AssetSummary{{
				AssetID: "2598", Title: "Dialogue Engine", Author: "Rubonnek",
				Category: "Tools", Cost: "MIT", VersionString: "1.6.0", GodotVersion: "4.2",
			}}}, nil
		},
	}
	t.Cleanup(func() { testAssetLibClient = nil })

	var out bytes.Buffer
	cmd := newRootCommand(&Options{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"assetlib", "search", "dialogue", "--dir", dir})
	require.NoError(t, cmd.Execute())

	require.Equal(t, "dialogue", got.Query)
	require.Equal(t, "4.2", got.GodotVersion)
	require.Contains(t, out.String(), "2598")
	require.Contains(t, out.String(), "Dialogue Engine")
}

func TestAssetLibSearchRequiresGodotVersion(t *testing.T) {
	dir := writeAssetLibProject(t, "")

	cmd := newRootCommand(&Options{})
	cmd.SetArgs([]string{"assetlib", "search", "dialogue", "--dir", dir})
	err := cmd.Execute()

	require.Error(t, err)
	var usageErr *UsageError
	require.ErrorAs(t, err, &usageErr)
	require.Contains(t, err.Error(), "--godot-version")
}

func TestAssetLibSearchWorksOutsideProjectWithExplicitGodotVersion(t *testing.T) {
	dir := t.TempDir()

	var got assetlib.SearchOptions
	testAssetLibClient = fakeAssetLibClient{
		search: func(_ context.Context, opts assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			got = opts
			return assetlib.SearchResponse{Results: []assetlib.AssetSummary{{AssetID: "2598", Title: "Dialogue Engine"}}}, nil
		},
	}
	t.Cleanup(func() { testAssetLibClient = nil })

	var out bytes.Buffer
	cmd := newRootCommand(&Options{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"assetlib", "search", "dialogue", "--dir", dir, "--godot-version", "4.6"})
	require.NoError(t, cmd.Execute())

	require.Equal(t, "dialogue", got.Query)
	require.Equal(t, "4.6", got.GodotVersion)
	require.Contains(t, out.String(), "2598")
}

func TestAssetLibSearchDoesNotRequireManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), []byte(`config/features=PackedStringArray("4.2")`), 0o644))

	var got assetlib.SearchOptions
	testAssetLibClient = fakeAssetLibClient{
		search: func(_ context.Context, opts assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			got = opts
			return assetlib.SearchResponse{}, nil
		},
	}
	t.Cleanup(func() { testAssetLibClient = nil })

	cmd := newRootCommand(&Options{})
	cmd.SetArgs([]string{"assetlib", "search", "dialogue", "--dir", dir})
	require.NoError(t, cmd.Execute())

	require.Equal(t, "4.2", got.GodotVersion)
}

func TestAssetLibSearchUsesLatestStableWhenProjectMissing(t *testing.T) {
	dir := t.TempDir()
	originalLatest := latestStableGodotVersion
	latestStableGodotVersion = func(context.Context) (string, error) {
		return "4.6", nil
	}
	t.Cleanup(func() { latestStableGodotVersion = originalLatest })

	var got assetlib.SearchOptions
	testAssetLibClient = fakeAssetLibClient{
		search: func(_ context.Context, opts assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			got = opts
			return assetlib.SearchResponse{}, nil
		},
	}
	t.Cleanup(func() { testAssetLibClient = nil })

	cmd := newRootCommand(&Options{})
	cmd.SetArgs([]string{"assetlib", "search", "dialogue", "--dir", dir})
	require.NoError(t, cmd.Execute())

	require.Equal(t, "4.6", got.GodotVersion)
}

func TestAssetLibSearchRejectsInvalidFilters(t *testing.T) {
	dir := writeAssetLibProject(t, `config/features=PackedStringArray("4.2")`)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "support", args: []string{"assetlib", "search", "dialogue", "--dir", dir, "--support", "cromulent"}, want: "--support"},
		{name: "sort", args: []string{"assetlib", "search", "dialogue", "--dir", dir, "--sort", "sideways"}, want: "--sort"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testAssetLibClient = fakeAssetLibClient{
				search: func(context.Context, assetlib.SearchOptions) (assetlib.SearchResponse, error) {
					t.Fatal("search should not be called for invalid filters")
					return assetlib.SearchResponse{}, nil
				},
			}
			t.Cleanup(func() { testAssetLibClient = nil })

			cmd := newRootCommand(&Options{})
			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			require.Error(t, err)
			var usageErr *UsageError
			require.ErrorAs(t, err, &usageErr)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAssetLibSearchJSON(t *testing.T) {
	dir := writeAssetLibProject(t, `config/features=PackedStringArray("4.2")`)
	testAssetLibClient = fakeAssetLibClient{
		search: func(_ context.Context, _ assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			return assetlib.SearchResponse{Results: []assetlib.AssetSummary{{AssetID: "2598", Title: "Dialogue Engine"}}}, nil
		},
	}
	t.Cleanup(func() { testAssetLibClient = nil })

	var out bytes.Buffer
	cmd := newRootCommand(&Options{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json", "assetlib", "search", "dialogue", "--dir", dir})
	require.NoError(t, cmd.Execute())

	require.JSONEq(t, `[{"asset_id":"2598","title":"Dialogue Engine","author":"","author_id":"","category":"","category_id":"","godot_version":"","rating":"","cost":"","support_level":"","icon_url":"","version":"","version_string":"","modify_date":""}]`, out.String())
}

func TestAssetLibSearchShowsNoResultsMessage(t *testing.T) {
	dir := writeAssetLibProject(t, `config/features=PackedStringArray("4.2")`)
	testAssetLibClient = fakeAssetLibClient{
		search: func(context.Context, assetlib.SearchOptions) (assetlib.SearchResponse, error) {
			return assetlib.SearchResponse{}, nil
		},
	}
	t.Cleanup(func() { testAssetLibClient = nil })

	var out bytes.Buffer
	cmd := newRootCommand(&Options{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"assetlib", "search", "definitely-not-real", "--dir", dir})
	require.NoError(t, cmd.Execute())

	require.Contains(t, out.String(), "no AssetLib results matched")
}

func TestAssetLibAddWritesArchiveSpecAndInstalls(t *testing.T) {
	dir := writeAssetLibProject(t, "")
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	testAssetLibClient = fakeAssetLibClient{
		get: func(_ context.Context, id string) (assetlib.AssetDetail, error) {
			require.Equal(t, "2598", id)
			return assetlib.AssetDetail{
				AssetSummary: assetlib.AssetSummary{AssetID: "2598", Title: "Dialogue Engine", VersionString: "1.6.0"},
				Type:         "addon",
				DownloadURL:  "https://example.com/dialogue.zip",
				DownloadHash: hash,
			}, nil
		},
	}
	testFetcherFor = func(spec manifest.AddonSpec) (source.Fetcher, error) {
		require.Equal(t, manifest.SourceArchive, spec.Source)
		require.Equal(t, "https://example.com/dialogue.zip", spec.URL)
		require.Equal(t, hash, spec.Checksum)
		return fakeFetcher{version: "1.6.0"}, nil
	}
	t.Cleanup(func() { testAssetLibClient = nil; testFetcherFor = nil })

	cmd := newRootCommand(&Options{})
	cmd.SetArgs([]string{"assetlib", "add", "2598", "--dir", dir})
	require.NoError(t, cmd.Execute())

	m, err := manifest.Load(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.Equal(t, manifest.SourceArchive, m.Addons["dialogue_engine"].Source)
	require.Equal(t, "https://example.com/dialogue.zip", m.Addons["dialogue_engine"].URL)
	require.Equal(t, "1.6.0", m.Addons["dialogue_engine"].Version)
	require.Equal(t, hash, m.Addons["dialogue_engine"].Checksum)
	_, err = os.Stat(filepath.Join(dir, "addons", "dialogue_engine", "plugin.cfg"))
	require.NoError(t, err)
}

func TestAssetLibAddRejectsZeroAssetID(t *testing.T) {
	dir := writeAssetLibProject(t, "")

	cmd := newRootCommand(&Options{})
	cmd.SetArgs([]string{"assetlib", "add", "0", "--dir", dir})
	err := cmd.Execute()

	require.Error(t, err)
	var usageErr *UsageError
	require.ErrorAs(t, err, &usageErr)
	require.Contains(t, err.Error(), "positive")
}

func TestAssetLibSpecRejectsUnsupportedDownloadURLScheme(t *testing.T) {
	_, err := assetLibSpecFromDetail(assetlib.AssetDetail{
		AssetSummary: assetlib.AssetSummary{AssetID: "2598", Title: "Dialogue Engine"},
		Type:         "addon",
		DownloadURL:  "ftp://example.com/dialogue.zip",
	}, &assetLibFlags{})

	require.Error(t, err)
	var usageErr *UsageError
	require.ErrorAs(t, err, &usageErr)
	require.Contains(t, err.Error(), "http:// or https://")
}

func TestAssetLibSpecIgnoresUnsupportedDownloadHash(t *testing.T) {
	spec, err := assetLibSpecFromDetail(assetlib.AssetDetail{
		AssetSummary: assetlib.AssetSummary{AssetID: "2598", Title: "Dialogue Engine"},
		Type:         "addon",
		DownloadURL:  "https://example.com/dialogue.zip",
		DownloadHash: "not-a-sha256",
	}, &assetLibFlags{})

	require.NoError(t, err)
	require.Empty(t, spec.Checksum)
}

func TestAssetLibAddRejectsDuplicateDerivedName(t *testing.T) {
	dir := writeAssetLibProject(t, "")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n[addons.dialogue_engine]\nsource=\"archive\"\nurl=\"https://example.com/existing.zip\"\n"), 0o644))

	testAssetLibClient = fakeAssetLibClient{
		get: func(context.Context, string) (assetlib.AssetDetail, error) {
			return assetlib.AssetDetail{
				AssetSummary: assetlib.AssetSummary{AssetID: "2598", Title: "Dialogue Engine"},
				Type:         "addon",
				DownloadURL:  "https://example.com/dialogue.zip",
			}, nil
		},
	}
	t.Cleanup(func() { testAssetLibClient = nil })

	cmd := newRootCommand(&Options{})
	cmd.SetArgs([]string{"assetlib", "add", "2598", "--dir", dir})
	err := cmd.Execute()

	require.Error(t, err)
	var usageErr *UsageError
	require.ErrorAs(t, err, &usageErr)
	require.Contains(t, err.Error(), "--name")
}

func TestAssetLibAddRollsBackManifestOnInstallFailure(t *testing.T) {
	dir := writeAssetLibProject(t, "")
	fetchErr := errors.New("fetch failed")
	testAssetLibClient = fakeAssetLibClient{
		get: func(context.Context, string) (assetlib.AssetDetail, error) {
			return assetlib.AssetDetail{
				AssetSummary: assetlib.AssetSummary{AssetID: "2598", Title: "Dialogue Engine"},
				Type:         "addon",
				DownloadURL:  "https://example.com/dialogue.zip",
			}, nil
		},
	}
	testFetcherFor = func(manifest.AddonSpec) (source.Fetcher, error) {
		return nil, fetchErr
	}
	t.Cleanup(func() { testAssetLibClient = nil; testFetcherFor = nil })

	cmd := newRootCommand(&Options{})
	cmd.SetArgs([]string{"assetlib", "add", "2598", "--dir", dir})
	err := cmd.Execute()

	require.ErrorIs(t, err, fetchErr)
	m, loadErr := manifest.Load(filepath.Join(dir, "addons.toml"))
	require.NoError(t, loadErr)
	require.NotContains(t, m.Addons, "dialogue_engine")
}

func TestAssetLibInteractiveUsesTUISelection(t *testing.T) {
	dir := writeAssetLibProject(t, `config/features=PackedStringArray("4.2")`)
	testAssetLibClient = fakeAssetLibClient{
		configure: func(_ context.Context, assetType string) (assetlib.Configuration, error) {
			require.Equal(t, "addon", assetType)
			return assetlib.Configuration{Categories: []assetlib.Category{{ID: "5", Name: "Tools"}}}, nil
		},
	}
	runAssetLibTUI = func(config tui.AssetLibConfig) (tui.AssetLibSelection, error) {
		require.Equal(t, "4.2", config.InitialGodotVersion)
		require.NotNil(t, config.Configure)
		configure, err := config.Configure(context.Background(), "addon")
		require.NoError(t, err)
		require.Equal(t, "Tools", configure.Categories[0].Name)
		return tui.AssetLibSelection{
			AssetID: "2598",
			Detail: assetlib.AssetDetail{
				AssetSummary: assetlib.AssetSummary{AssetID: "2598", Title: "Dialogue Engine", VersionString: "1.6.0"},
				Type:         "addon",
				DownloadURL:  "https://example.com/dialogue.zip",
			},
		}, nil
	}
	testFetcherFor = func(manifest.AddonSpec) (source.Fetcher, error) {
		return fakeFetcher{version: "1.6.0"}, nil
	}
	t.Cleanup(func() { runAssetLibTUI = tui.RunAssetLibBrowser; testAssetLibClient = nil; testFetcherFor = nil })

	cmd := newRootCommand(&Options{})
	cmd.SetArgs([]string{"assetlib", "--dir", dir})
	require.NoError(t, cmd.Execute())

	m, err := manifest.Load(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.Contains(t, m.Addons, "dialogue_engine")
}

func TestAssetLibInteractiveUsesLatestStableBrowseOnlyWhenProjectMissing(t *testing.T) {
	dir := t.TempDir()
	originalLatest := latestStableGodotVersion
	latestStableGodotVersion = func(context.Context) (string, error) {
		return "4.6", nil
	}
	runAssetLibTUI = func(config tui.AssetLibConfig) (tui.AssetLibSelection, error) {
		require.Equal(t, "4.6", config.InitialGodotVersion)
		require.Contains(t, config.InstallDisabledReason, "no project.godot")
		return tui.AssetLibSelection{}, tui.ErrAssetLibCancelled
	}
	t.Cleanup(func() {
		latestStableGodotVersion = originalLatest
		runAssetLibTUI = tui.RunAssetLibBrowser
	})

	cmd := newRootCommand(&Options{})
	cmd.SetArgs([]string{"assetlib", "--dir", dir})
	err := cmd.Execute()

	require.Error(t, err)
	require.Contains(t, err.Error(), "no project.godot")
}
