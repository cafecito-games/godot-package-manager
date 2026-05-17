package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestGitHubReleaseFetch(t *testing.T) {
	payload := zipBytes(t, map[string]string{"plugin.cfg": "[plugin]"})
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "addon.zip", "url": "ASSETURL"},
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

func TestGitHubReleaseDownloadsAssetViaAPIURL(t *testing.T) {
	payload := zipBytes(t, map[string]string{"plugin.cfg": "[plugin]"})
	var assetAccept string

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		// Private-repo releases: only the API asset URL is downloadable; the
		// browser_download_url returns 404 even with a valid token.
		assetAPIURL := "http://" + r.Host + "/repos/owner/repo/releases/assets/42"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "addon.zip", "url": assetAPIURL, "browser_download_url": "http://" + r.Host + "/private-404"},
			},
		})
	})
	mux.HandleFunc("/repos/owner/repo/releases/assets/42", func(w http.ResponseWriter, r *http.Request) {
		assetAccept = r.Header.Get("Accept")
		_, _ = w.Write(payload)
	})
	mux.HandleFunc("/private-404", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := &GitHubReleaseFetcher{APIBase: srv.URL}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGitHubRelease, Repo: "owner/repo", Version: "1.0", Asset: "addon.zip",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	require.Equal(t, "application/octet-stream", assetAccept)
}

func TestGitHubReleaseAmbiguousAsset(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "a.zip", "url": "u1"},
				{"name": "b.zip", "url": "u2"},
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
				{"name": "a.zip", "url": "u1"},
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
				{"name": "addon.zip", "url": "ASSETURL"},
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
				{"name": "addon.zip", "url": "ASSETURL"},
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

func TestGitHubReleaseEscapesTagPathComponent(t *testing.T) {
	payload := zipBytes(t, map[string]string{"plugin.cfg": "[plugin]"})
	var gotReleasePath string

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotReleasePath = r.URL.EscapedPath()
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "addon.zip", "url": "ASSETURL"},
			},
		})
	})
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := &GitHubReleaseFetcher{APIBase: srv.URL, assetURLRewrite: func(string) string { return srv.URL + "/asset.zip" }}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGitHubRelease, Repo: "owner/repo", Version: "release/v1", Asset: "addon.zip",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	_, err = os.Stat(filepath.Join(res.Dir, "plugin.cfg"))
	require.NoError(t, err)
	require.Equal(t, "/repos/owner/repo/releases/tags/release%2Fv1", gotReleasePath)
}
