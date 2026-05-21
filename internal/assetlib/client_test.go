package assetlib_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/assetlib"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestSearchBuildsAssetQuery(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[{"asset_id":"2598","title":"Dialogue Engine","author":"Rubonnek","category":"Tools","category_id":"5","godot_version":"4.2","cost":"MIT","support_level":"community","version":"12","version_string":"1.6.0","modify_date":"2026-02-27 22:05:18"}],"page":0,"pages":1,"page_length":1,"total_items":1}`))
	}))
	defer server.Close()

	client := assetlib.NewClient(server.URL)
	result, err := client.Search(context.Background(), assetlib.SearchOptions{
		Query:        "dialogue",
		GodotVersion: "4.2",
		MaxResults:   1,
		Support:      "community",
		Sort:         "updated",
	})

	require.NoError(t, err)
	require.Equal(t, "/asset", gotPath)
	require.Equal(t, "addon", gotQuery.Get("type"))
	require.Equal(t, "dialogue", gotQuery.Get("filter"))
	require.Equal(t, "4.2", gotQuery.Get("godot_version"))
	require.Equal(t, "1", gotQuery.Get("max_results"))
	require.Equal(t, "community", gotQuery.Get("support"))
	require.Equal(t, "updated", gotQuery.Get("sort"))
	require.Equal(t, "2598", result.Results[0].AssetID)
	require.Equal(t, "Dialogue Engine", result.Results[0].Title)
}

func TestConfigureGetsCategories(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/configure", r.URL.Path)
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"categories":[{"id":"5","name":"Tools","type":"0"}]}`))
	}))
	defer server.Close()

	client := assetlib.NewClient(server.URL)
	config, err := client.Configure(context.Background(), "addon")

	require.NoError(t, err)
	require.Equal(t, "addon", gotQuery.Get("type"))
	require.Len(t, config.Categories, 1)
	require.Equal(t, "Tools", config.Categories[0].Name)
}

func TestGetAssetFetchesDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/asset/2598", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"asset_id":"2598","type":"addon","title":"Dialogue Engine","version_string":"1.6.0","download_url":"https://example.com/dialogue.zip","browse_url":"https://example.com/dialogue"}`))
	}))
	defer server.Close()

	client := assetlib.NewClient(server.URL)
	detail, err := client.GetAsset(context.Background(), "2598")

	require.NoError(t, err)
	require.Equal(t, "2598", detail.AssetID)
	require.Equal(t, "addon", detail.Type)
	require.Equal(t, "https://example.com/dialogue.zip", detail.DownloadURL)
}

func TestClientWrapsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := assetlib.NewClient(server.URL)
	_, err := client.GetAsset(context.Background(), "2598")

	require.Error(t, err)
	var fetchErr *output.FetchError
	require.ErrorAs(t, err, &fetchErr)
}

func TestClientWrapsMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{`))
	}))
	defer server.Close()

	client := assetlib.NewClient(server.URL)
	_, err := client.Search(context.Background(), assetlib.SearchOptions{Query: "dialogue"})

	require.Error(t, err)
	var fetchErr *output.FetchError
	require.ErrorAs(t, err, &fetchErr)
}

func TestLatestStableGodotVersionParsesGitHubReleases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"tag_name":"4.7-beta1","draft":false,"prerelease":true},
			{"tag_name":"4.6.3-stable","draft":false,"prerelease":false}
		]`))
	}))
	defer server.Close()

	version, err := assetlib.LatestStableGodotVersion(context.Background(), server.URL)

	require.NoError(t, err)
	require.Equal(t, "4.6", version)
}

func TestLatestStableGodotVersionUsesClientConfiguration(t *testing.T) {
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"tag_name":"4.6.3-stable","draft":false,"prerelease":false}]`))
	}))
	defer server.Close()

	client := assetlib.NewClient("")
	client.HTTPClient = server.Client()
	client.MaxBytes = 128

	version, err := client.LatestStableGodotVersion(context.Background(), server.URL)

	require.NoError(t, err)
	require.Equal(t, "4.6", version)
	require.NotEmpty(t, userAgent)
}

func TestLatestStableGodotVersionRespectsClientMaxBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"tag_name":"4.6.3-stable","draft":false,"prerelease":false}]`))
	}))
	defer server.Close()

	client := assetlib.NewClient("")
	client.HTTPClient = server.Client()
	client.MaxBytes = 8

	_, err := client.LatestStableGodotVersion(context.Background(), server.URL)

	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum size")
}

func TestLatestStableGodotVersionSkipsDraftsAndPrereleases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"tag_name":"4.8-stable","draft":true,"prerelease":false},
			{"tag_name":"4.7-rc1","draft":false,"prerelease":true},
			{"tag_name":"4.6.3-stable","draft":false,"prerelease":false}
		]`))
	}))
	defer server.Close()

	version, err := assetlib.LatestStableGodotVersion(context.Background(), server.URL)

	require.NoError(t, err)
	require.Equal(t, "4.6", version)
}

func TestManifestNameFromTitle(t *testing.T) {
	require.Equal(t, "dialogue_manager", assetlib.ManifestNameFromTitle("Dialogue Manager"))
	require.Equal(t, "café_engine", assetlib.ManifestNameFromTitle("Café Engine"))
	require.Equal(t, "", assetlib.ManifestNameFromTitle("!!!"))
}

func TestClientSendsJSONHeaders(t *testing.T) {
	var accept, userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	defer server.Close()

	client := assetlib.NewClient(server.URL)
	_, err := client.Search(context.Background(), assetlib.SearchOptions{Query: "dialogue"})

	require.NoError(t, err)
	require.Equal(t, "application/json", accept)
	require.NotEmpty(t, userAgent)
	require.NotEqual(t, "Go-http-client/1.1", userAgent)
}

func TestClientRejectsUnexpectedContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html>maintenance</html>`))
	}))
	defer server.Close()

	client := assetlib.NewClient(server.URL)
	_, err := client.Search(context.Background(), assetlib.SearchOptions{Query: "dialogue"})

	require.Error(t, err)
	var fetchErr *output.FetchError
	require.ErrorAs(t, err, &fetchErr)
	require.Contains(t, err.Error(), "expected JSON")
	require.Contains(t, err.Error(), "text/html")
}

func TestClientRejectsMalformedContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=\"unterminated")
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	defer server.Close()

	client := assetlib.NewClient(server.URL)
	_, err := client.Search(context.Background(), assetlib.SearchOptions{Query: "dialogue"})

	require.Error(t, err)
	var fetchErr *output.FetchError
	require.ErrorAs(t, err, &fetchErr)
	require.Contains(t, err.Error(), "expected JSON")
}
