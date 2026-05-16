package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/cafecito-games/godot-addon-manager/internal/manifest"
	"github.com/cafecito-games/godot-addon-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestGitHubReleaseFetch(t *testing.T) {
	payload := zipBytes(t, map[string]string{"plugin.cfg": "[plugin]"})
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "addon.zip", "browser_download_url": "ASSETURL"},
			},
		})
	})
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := &GitHubReleaseFetcher{APIBase: srv.URL, assetURLRewrite: func(string) string { return srv.URL + "/asset.zip" }}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGitHubRelease, Repo: "owner/repo", Version: "1.0", Asset: "addon.zip",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	require.NotEmpty(t, res.Checksum)
	require.Equal(t, "1.0", res.ResolvedVersion)
}

func TestGitHubReleaseAmbiguousAsset(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "a.zip", "browser_download_url": "u1"},
				{"name": "b.zip", "browser_download_url": "u2"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f := &GitHubReleaseFetcher{APIBase: srv.URL}
	_, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGitHubRelease, Repo: "o/r", Version: "1.0",
	})
	require.Error(t, err)
	var fetchErr *output.FetchError
	require.ErrorAs(t, err, &fetchErr)
}

func TestGitHubReleaseNoMatchingAsset(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "a.zip", "browser_download_url": "u1"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f := &GitHubReleaseFetcher{APIBase: srv.URL}
	_, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGitHubRelease, Repo: "o/r", Version: "1.0", Asset: "nomatch.zip",
	})
	require.Error(t, err)
	var fetchErr *output.FetchError
	require.ErrorAs(t, err, &fetchErr)
}

func TestGitHubReleaseSoleAsset(t *testing.T) {
	payload := zipBytes(t, map[string]string{"plugin.cfg": "[plugin]"})
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "addon.zip", "browser_download_url": "ASSETURL"},
			},
		})
	})
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := &GitHubReleaseFetcher{APIBase: srv.URL, assetURLRewrite: func(string) string { return srv.URL + "/asset.zip" }}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGitHubRelease, Repo: "owner/repo", Version: "1.0", Asset: "",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	require.NotEmpty(t, res.Checksum)
	require.Equal(t, "1.0", res.ResolvedVersion)
}

func TestGitHubReleaseSendsToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret123")

	payload := zipBytes(t, map[string]string{"plugin.cfg": "[plugin]"})
	var receivedAuthHeader string

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "addon.zip", "browser_download_url": "ASSETURL"},
			},
		})
	})
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := &GitHubReleaseFetcher{APIBase: srv.URL, assetURLRewrite: func(string) string { return srv.URL + "/asset.zip" }}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGitHubRelease, Repo: "owner/repo", Version: "1.0", Asset: "addon.zip",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	require.Equal(t, "Bearer secret123", receivedAuthHeader)
}
