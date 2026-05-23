package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
)

const defaultGitHubAPIBase = "https://api.github.com"

// GitHubReleaseFetcher downloads an asset from a GitHub release.
type GitHubReleaseFetcher struct {
	// APIBase overrides the GitHub API root; empty means the public API.
	APIBase string
	// client overrides the HTTP client; nil uses defaultHTTPClient.
	client *http.Client
	// assetURLRewrite, if set, rewrites an asset download URL (used in tests).
	assetURLRewrite func(string) string
	// maxBytes overrides the download size cap; 0 uses defaultMaxDownloadBytes.
	maxBytes int64
	// maxExtracted overrides the extracted size cap; 0 uses maxExtractedBytes.
	maxExtracted int64
}

type ghAsset struct {
	Name string `json:"name"`
	// APIURL is the asset's REST endpoint. Downloading it with an
	// `Accept: application/octet-stream` header works for both public and
	// private repositories, whereas browser_download_url returns 404 for
	// private repos even with a valid token.
	APIURL string `json:"url"`
}

type ghRelease struct {
	Assets []ghAsset `json:"assets"`
}

// Fetch resolves the release tag, selects the matching asset, and extracts it.
func (f *GitHubReleaseFetcher) Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error) {
	base := f.APIBase
	if base == "" {
		base = defaultGitHubAPIBase
	}
	repoParts := strings.Split(spec.Repo, "/")
	if len(repoParts) != 2 || repoParts[0] == "" || repoParts[1] == "" {
		return FetchResult{}, &output.FetchError{Err: fmt.Errorf("github-release repo %q must be owner/repo", spec.Repo)}
	}
	apiURL := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s",
		strings.TrimRight(base, "/"),
		url.PathEscape(repoParts[0]),
		url.PathEscape(repoParts[1]),
		url.PathEscape(spec.Version))
	body, err := download(ctx, f.client, apiURL, githubHeader("application/vnd.github+json"), f.maxBytes)
	if err != nil {
		return FetchResult{}, annotateGitHubError(err)
	}
	var release ghRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return FetchResult{}, &output.FetchError{Err: fmt.Errorf("parsing release JSON: %w", err)}
	}
	asset, err := selectAsset(release.Assets, spec.Asset)
	if err != nil {
		return FetchResult{}, err
	}
	downloadURL := asset.APIURL
	if f.assetURLRewrite != nil {
		downloadURL = f.assetURLRewrite(downloadURL)
	}
	// Asset bytes are served from the asset's API URL; the octet-stream Accept
	// header tells GitHub to return the binary rather than the asset's JSON.
	archivePath, checksum, err := downloadToFile(ctx, f.client, downloadURL, githubHeader("application/octet-stream"), f.maxBytes)
	if err != nil {
		return FetchResult{}, err
	}
	defer func() { _ = os.Remove(archivePath) }()

	dir, err := os.MkdirTemp("", "gpm-ghrel-*")
	if err != nil {
		return FetchResult{}, &output.FetchError{Err: err}
	}
	if err := extractArchive(asset.Name, archivePath, dir, f.maxExtracted); err != nil {
		_ = os.RemoveAll(dir)
		return FetchResult{}, err
	}
	return FetchResult{
		Dir:             dir,
		ResolvedVersion: spec.Version,
		Checksum:        checksum,
	}, nil
}

// selectAsset chooses the asset matching pattern. An empty pattern requires
// exactly one asset. A non-empty pattern uses path.Match glob semantics.
func selectAsset(assets []ghAsset, pattern string) (ghAsset, error) {
	var matches []ghAsset
	for _, asset := range assets {
		if pattern == "" {
			matches = append(matches, asset)
			continue
		}
		if ok, _ := path.Match(pattern, asset.Name); ok {
			matches = append(matches, asset)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return ghAsset{}, &output.FetchError{Err: fmt.Errorf("no release asset matched %q", pattern)}
	default:
		return ghAsset{}, &output.FetchError{Err: fmt.Errorf("multiple release assets matched %q; set `asset` to disambiguate", pattern)}
	}
}

// githubToken returns the GitHub API token from the environment, preferring
// GITHUB_TOKEN and falling back to GH_TOKEN. The result is empty when neither
// is set.
func githubToken() string {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	return strings.TrimSpace(token)
}

// githubHeader returns request headers with the given Accept value, including
// auth when a token is present in the environment.
func githubHeader(accept string) http.Header {
	header := http.Header{}
	header.Set("Accept", accept)
	if token := githubToken(); token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	return header
}

// annotateGitHubError adds a hint about authentication when a request fails
// with HTTP 404 and no token is configured. GitHub returns 404 (not 403) for
// private repositories that the request cannot see, so an unauthenticated 404
// is most often a missing token rather than a genuinely absent release.
func annotateGitHubError(err error) error {
	if err == nil {
		return nil
	}
	if githubToken() == "" && strings.Contains(err.Error(), "HTTP 404") {
		return &output.FetchError{Err: fmt.Errorf(
			"%w\nif this is a private repository, set GITHUB_TOKEN to authenticate "+
				"(e.g. export GITHUB_TOKEN=$(gh auth token))", err)}
	}
	return err
}
