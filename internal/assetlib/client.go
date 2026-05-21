package assetlib

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

const (
	DefaultBaseURL          = "https://godotengine.org/asset-library/api"
	DefaultGodotReleasesURL = "https://api.github.com/repos/godotengine/godot/releases?per_page=30"
	DefaultLatestStableURL  = DefaultGodotReleasesURL
	defaultMaxBytes         = 4 << 20
	defaultTimeout          = 30 * time.Second
	userAgent               = "gpm godot-addon-manager"
)

var stableReleaseTagPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)(?:\.[0-9]+)?-stable$`)

// Client calls the Godot Asset Library API.
type Client struct {
	BaseURL string

	// HTTPClient is used for AssetLib and latest-version requests. When nil,
	// requests use a default client with a 30 second timeout. Configure this
	// before sharing a Client across goroutines.
	HTTPClient *http.Client

	// MaxBytes limits API response bodies. Values <= 0 use the default 4 MiB
	// cap. Configure this before sharing a Client across goroutines.
	MaxBytes int64
}

type godotRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Configuration is the /configure response subset used by gpm.
type Configuration struct {
	Categories []Category `json:"categories"`
}

// Category is one Asset Library category.
type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// SearchOptions are the supported filters for GET /asset.
type SearchOptions struct {
	Query        string
	GodotVersion string
	Category     string
	Support      string
	Sort         string
	MaxResults   int
	Page         int
}

// AssetSummary is one result returned by GET /asset.
type AssetSummary struct {
	AssetID       string `json:"asset_id"`
	Title         string `json:"title"`
	Author        string `json:"author"`
	AuthorID      string `json:"author_id"`
	Category      string `json:"category"`
	CategoryID    string `json:"category_id"`
	GodotVersion  string `json:"godot_version"`
	Rating        string `json:"rating"`
	Cost          string `json:"cost"`
	SupportLevel  string `json:"support_level"`
	IconURL       string `json:"icon_url"`
	Version       string `json:"version"`
	VersionString string `json:"version_string"`
	ModifyDate    string `json:"modify_date"`
}

// SearchResponse is the GET /asset response.
type SearchResponse struct {
	Results    []AssetSummary `json:"result"`
	Page       int            `json:"page"`
	Pages      int            `json:"pages"`
	PageLength int            `json:"page_length"`
	TotalItems int            `json:"total_items"`
}

// AssetDetail is the GET /asset/{id} response subset used by gpm.
type AssetDetail struct {
	AssetSummary
	Type             string `json:"type"`
	Description      string `json:"description"`
	DownloadProvider string `json:"download_provider"`
	DownloadCommit   string `json:"download_commit"`
	DownloadHash     string `json:"download_hash"`
	DownloadURL      string `json:"download_url"`
	BrowseURL        string `json:"browse_url"`
	IssuesURL        string `json:"issues_url"`
	Searchable       string `json:"searchable"`
}

// NewClient creates a Client using baseURL, or the official Asset Library API
// when baseURL is empty.
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		MaxBytes:   defaultMaxBytes,
	}
}

// LatestStableGodotVersion returns the current stable major.minor Godot series
// by reading the official Godot GitHub releases API.
func LatestStableGodotVersion(ctx context.Context, releasesURL string) (string, error) {
	return NewClient("").LatestStableGodotVersion(ctx, releasesURL)
}

// LatestStableGodotVersion returns the current stable major.minor Godot series
// using the client HTTP settings.
func (c *Client) LatestStableGodotVersion(ctx context.Context, releasesURL string) (string, error) {
	if releasesURL == "" {
		releasesURL = DefaultGodotReleasesURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return "", &output.FetchError{Err: err}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", &output.FetchError{Err: fmt.Errorf("fetching latest Godot stable version: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", &output.FetchError{Err: fmt.Errorf("fetching latest Godot stable version: HTTP %d", resp.StatusCode)}
	}
	if err := requireJSONContentType(releasesURL, resp.Header.Get("Content-Type")); err != nil {
		return "", err
	}
	maxBytes := c.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	// Read one byte past the limit so oversized responses can be detected
	// without reading the rest of the body into memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", &output.FetchError{Err: err}
	}
	if int64(len(body)) > maxBytes {
		return "", &output.FetchError{Err: fmt.Errorf("latest Godot stable version response exceeds maximum size of %d bytes", maxBytes)}
	}
	var releases []godotRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return "", &output.FetchError{Err: fmt.Errorf("decoding Godot releases response: %w", err)}
	}
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		match := stableReleaseTagPattern.FindStringSubmatch(release.TagName)
		if len(match) == 3 {
			return match[1] + "." + match[2], nil
		}
	}
	return "", &output.FetchError{Err: fmt.Errorf("latest Godot stable version not found")}
}

// ManifestNameFromTitle derives a manifest-safe addon key from an AssetLib
// title. It preserves Unicode letters and digits, converts separators to
// underscores, and returns an empty string when no usable name can be derived.
func ManifestNameFromTitle(title string) string {
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastUnderscore = false
		case unicode.IsSpace(r) || r == '-' || r == '_':
			if builder.Len() > 0 && !lastUnderscore {
				builder.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(builder.String(), "_")
}

// Configure fetches Asset Library metadata such as categories.
func (c *Client) Configure(ctx context.Context, assetType string) (Configuration, error) {
	values := url.Values{}
	if assetType != "" {
		values.Set("type", assetType)
	}
	var out Configuration
	if err := c.getJSON(ctx, []string{"configure"}, values, &out); err != nil {
		return Configuration{}, err
	}
	return out, nil
}

// Search lists Asset Library assets.
func (c *Client) Search(ctx context.Context, opts SearchOptions) (SearchResponse, error) {
	values := url.Values{}
	values.Set("type", "addon")
	if opts.Query != "" {
		values.Set("filter", opts.Query)
	}
	if opts.GodotVersion != "" {
		values.Set("godot_version", opts.GodotVersion)
	}
	if opts.Category != "" {
		values.Set("category", opts.Category)
	}
	if opts.Support != "" {
		values.Set("support", opts.Support)
	}
	if opts.Sort != "" {
		values.Set("sort", opts.Sort)
	}
	if opts.MaxResults > 0 {
		values.Set("max_results", strconv.Itoa(opts.MaxResults))
	}
	if opts.Page > 0 {
		values.Set("page", strconv.Itoa(opts.Page))
	}
	var out SearchResponse
	if err := c.getJSON(ctx, []string{"asset"}, values, &out); err != nil {
		return SearchResponse{}, err
	}
	return out, nil
}

// GetAsset fetches one Asset Library asset by id.
func (c *Client) GetAsset(ctx context.Context, id string) (AssetDetail, error) {
	var out AssetDetail
	if err := c.getJSON(ctx, []string{"asset", id}, nil, &out); err != nil {
		return AssetDetail{}, err
	}
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, parts []string, values url.Values, target any) error {
	rawURL, err := c.url(parts, values)
	if err != nil {
		return err
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return &output.FetchError{Err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return &output.FetchError{Err: fmt.Errorf("assetlib request %s: %w", rawURL, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &output.FetchError{Err: fmt.Errorf("assetlib request %s: HTTP %d", rawURL, resp.StatusCode)}
	}
	if err := requireJSONContentType(rawURL, resp.Header.Get("Content-Type")); err != nil {
		return err
	}
	maxBytes := c.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	// Read one byte past the limit so oversized responses can be detected
	// without reading the rest of the body into memory.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return &output.FetchError{Err: err}
	}
	if int64(len(body)) > maxBytes {
		return &output.FetchError{Err: fmt.Errorf("assetlib response exceeds maximum size of %d bytes", maxBytes)}
	}
	if err := json.Unmarshal(body, target); err != nil {
		return &output.FetchError{Err: fmt.Errorf("decoding assetlib response: %w", err)}
	}
	return nil
}

func requireJSONContentType(rawURL, contentType string) error {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		if contentType == "" {
			contentType = "missing content type"
		}
		return &output.FetchError{Err: fmt.Errorf("assetlib request %s: expected JSON response, got %s", rawURL, contentType)}
	}
	return nil
}

func (c *Client) url(parts []string, values url.Values) (string, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", &output.FetchError{Err: fmt.Errorf("invalid assetlib base URL %q: %w", c.BaseURL, err)}
	}
	pathParts := append([]string{base.Path}, parts...)
	base.Path = path.Join(pathParts...)
	base.RawQuery = values.Encode()
	return base.String(), nil
}
