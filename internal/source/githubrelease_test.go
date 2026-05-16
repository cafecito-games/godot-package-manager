package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
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
	require.True(t, strings.Contains(err.Error(), "asset"))
}
