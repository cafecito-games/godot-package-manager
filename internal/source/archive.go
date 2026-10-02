package source

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
)

const (
	// defaultMaxDownloadBytes caps the size of a downloaded (compressed) payload.
	defaultMaxDownloadBytes int64 = 512 << 20
	// httpTimeout bounds a single HTTP request.
	httpTimeout = 60 * time.Second
)

// Extraction limits guard against decompression-bomb disk exhaustion. They are
// variables rather than constants only so tests can exercise the limits cheaply.
var (
	// maxExtractedBytes caps the total uncompressed size of an extracted archive.
	maxExtractedBytes int64 = 1 << 30
	// maxExtractedFiles caps the number of entries an archive may contain.
	maxExtractedFiles = 20000
)

// defaultHTTPClient is the shared client used when a fetcher does not provide
// its own. It is treated as immutable.
var defaultHTTPClient = &http.Client{Timeout: httpTimeout}

// ArchiveFetcher downloads and extracts a plain zip or tarball URL.
type ArchiveFetcher struct {
	// client overrides the HTTP client; nil uses defaultHTTPClient.
	client *http.Client
	// maxBytes overrides the download size cap; 0 uses defaultMaxDownloadBytes.
	maxBytes int64
	// maxExtracted overrides the extracted size cap; 0 uses maxExtractedBytes.
	maxExtracted int64
}

// Fetch downloads spec.URL, extracts it into a new temp directory, and reports
// the archive's SHA-256 checksum.
//
// When the manifest sets `index` the addon is sliced instead: the index names
// the archives, only the slices this project needs are downloaded, and the
// merged tree is still one directory, so nothing downstream changes.
func (f *ArchiveFetcher) Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error) {
	resolve := archiveSliceResolver(spec.Index)
	if indexURL, sliced := slicedIndexURL(spec, resolve); sliced {
		fetcher := &slicedFetcher{
			client:         f.client,
			maxBytes:       f.maxBytes,
			maxExtracted:   f.maxExtracted,
			resolve:        resolve,
			stagingPattern: "gpm-archive-*",
			diagnostics:    archiveSlicedDiagnostics(spec),
		}
		return fetcher.fetch(ctx, spec, indexURL)
	}

	archivePath, checksum, err := downloadToFile(ctx, f.client, spec.URL, nil, f.maxBytes)
	if err != nil {
		return FetchResult{}, err
	}
	defer func() { _ = os.Remove(archivePath) }()

	dir, err := os.MkdirTemp("", "gpm-archive-*")
	if err != nil {
		return FetchResult{}, &output.FetchError{Err: err}
	}
	if err := extractArchive(spec.URL, archivePath, dir, f.maxExtracted); err != nil {
		_ = os.RemoveAll(dir)
		return FetchResult{}, err
	}
	return FetchResult{
		Dir:             dir,
		ResolvedVersion: spec.Version,
		Checksum:        checksum,
	}, nil
}

// httpGet issues a GET request and returns the response on a 200 status. The
// caller must close the response body. header, if non-nil, is applied to the
// request.
func httpGet(ctx context.Context, client *http.Client, rawURL string, header http.Header) (*http.Response, error) {
	if client == nil {
		client = defaultHTTPClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &output.FetchError{Err: err}
	}
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &output.FetchError{Err: fmt.Errorf("downloading %s: %w", rawURL, err)}
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, &output.FetchError{Err: fmt.Errorf("downloading %s: HTTP %d", rawURL, resp.StatusCode)}
	}
	return resp, nil
}

// download performs an HTTP GET and returns the response body fully in memory.
// It is intended for small payloads such as API JSON. maxBytes <= 0 uses the
// default cap.
func download(ctx context.Context, client *http.Client, rawURL string, header http.Header, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxDownloadBytes
	}
	resp, err := httpGet(ctx, client, rawURL, header)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, &output.FetchError{Err: err}
	}
	if int64(len(body)) > maxBytes {
		return nil, &output.FetchError{Err: fmt.Errorf(
			"downloading %s: response exceeds maximum download size of %d bytes", rawURL, maxBytes)}
	}
	return body, nil
}

// downloadToFile streams an HTTP GET response to a temporary file, computing
// its SHA-256 along the way. It returns the file path and the hex-encoded
// checksum; the caller is responsible for removing the file. maxBytes <= 0 uses
// the default cap.
func downloadToFile(ctx context.Context, client *http.Client, rawURL string, header http.Header, maxBytes int64) (string, string, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxDownloadBytes
	}
	resp, err := httpGet(ctx, client, rawURL, header)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	tmp, err := os.CreateTemp("", "gpm-download-*")
	if err != nil {
		return "", "", &output.FetchError{Err: err}
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(resp.Body, maxBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmp.Name())
		return "", "", &output.FetchError{Err: copyErr}
	}
	if closeErr != nil {
		_ = os.Remove(tmp.Name())
		return "", "", &output.FetchError{Err: closeErr}
	}
	if written > maxBytes {
		_ = os.Remove(tmp.Name())
		return "", "", &output.FetchError{Err: fmt.Errorf(
			"downloading %s: response exceeds maximum download size of %d bytes", rawURL, maxBytes)}
	}
	return tmp.Name(), hex.EncodeToString(hasher.Sum(nil)), nil
}

// extractArchive extracts the archive at archivePath into dir under a guard of
// its own, which is the single-archive case every unsliced fetch takes.
// maxExtracted <= 0 uses the package default cap.
func extractArchive(nameHint, archivePath, dir string, maxExtracted int64) error {
	return extractArchiveInto(nameHint, archivePath, dir, newExtractGuard(maxExtracted), nil)
}

// extractArchiveInto extracts the archive at archivePath into dir, choosing zip
// vs tar.gz based on nameHint's file extension.
//
// The guard is the caller's rather than this function's, so several archives
// merged into one directory share one budget and the extracted-size cap means
// what the flag says instead of being multiplied by the number of archives.
//
// claim, when non-nil, is called with each regular file's path relative to dir
// before that file is written. A merge uses it to refuse two archives shipping
// one path rather than letting the last one extracted win, and because the check
// happens before the write, the refusal leaves the earlier archive's file as it
// was.
func extractArchiveInto(nameHint, archivePath, dir string, guard *extractGuard, claim func(relative string) error) error {
	archiveName := archiveNameForDetection(nameHint)
	switch {
	case strings.HasSuffix(archiveName, ".zip"):
		return extractZip(archivePath, dir, guard, claim)
	case strings.HasSuffix(archiveName, ".tar.gz"), strings.HasSuffix(archiveName, ".tgz"):
		return extractTarGz(archivePath, dir, guard, claim)
	default:
		return &output.FetchError{Err: fmt.Errorf("unsupported archive type: %s", nameHint)}
	}
}

func archiveNameForDetection(nameHint string) string {
	if parsed, err := url.Parse(nameHint); err == nil && parsed.Path != "" {
		return strings.ToLower(parsed.Path)
	}
	return strings.ToLower(nameHint)
}

// extractGuard enforces limits on entry count and total uncompressed size over
// everything extracted under it. One guard covers one archive for an unsliced
// fetch and every merged archive for a sliced one.
type extractGuard struct {
	files    int
	bytes    int64
	maxBytes int64
	// overBudget builds the error reported when a limit is exceeded. It exists
	// because the answer depends on what the budget bounds: one archive the
	// installer is unpacking, or a set of remote archives an index asked the
	// project to download.
	overBudget func(error) error
}

func newExtractGuard(maxBytes int64) *extractGuard {
	if maxBytes <= 0 {
		maxBytes = maxExtractedBytes
	}
	return &extractGuard{
		maxBytes:   maxBytes,
		overBudget: func(err error) error { return &output.InstallError{Err: err} },
	}
}

// newMergeExtractGuard builds the single guard a sliced merge shares across
// every slice archive it extracts. It reports a limit as an *output.FetchError
// rather than an *output.InstallError: the budget bounds the merged tree the
// index asked the project to download, so exceeding it says the publisher ships
// more than the project allows rather than that the local filesystem refused a
// write.
func newMergeExtractGuard(maxBytes int64) *extractGuard {
	guard := newExtractGuard(maxBytes)
	guard.overBudget = func(err error) error { return &output.FetchError{Err: err} }
	return guard
}

func (g *extractGuard) addFile() error {
	g.files++
	if g.files > maxExtractedFiles {
		return g.overBudget(fmt.Errorf("archive contains more than %d entries", maxExtractedFiles))
	}
	return nil
}

func (g *extractGuard) addBytes(n int64) error {
	g.bytes += n
	if g.bytes > g.maxBytes {
		return g.overBudget(fmt.Errorf(
			"archive expands beyond the maximum extracted size of %d bytes", g.maxBytes))
	}
	return nil
}

func extractZip(archivePath, dir string, guard *extractGuard, claim func(relative string) error) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return &output.InstallError{Err: err}
	}
	defer func() { _ = reader.Close() }()

	for _, zipFile := range reader.File {
		dest, err := safeJoin(dir, zipFile.Name)
		if err != nil {
			return err
		}
		if zipFile.Mode()&os.ModeSymlink != 0 {
			return &output.InstallError{Err: fmt.Errorf(
				"archive contains an unsupported symlink entry: %s", zipFile.Name)}
		}
		if zipFile.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return &output.InstallError{Err: err}
			}
			continue
		}
		if err := claimExtractedPath(claim, dir, dest); err != nil {
			return err
		}
		if err := guard.addFile(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return &output.InstallError{Err: err}
		}
		readCloser, err := zipFile.Open()
		if err != nil {
			return &output.InstallError{Err: err}
		}
		err = writeFile(dest, readCloser, zipFile.Mode(), guard)
		_ = readCloser.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(archivePath, dir string, guard *extractGuard, claim func(relative string) error) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return &output.InstallError{Err: err}
	}
	defer func() { _ = file.Close() }()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return &output.InstallError{Err: err}
	}
	defer func() { _ = gzipReader.Close() }()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return &output.InstallError{Err: err}
		}
		dest, err := safeJoin(dir, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return &output.InstallError{Err: err}
			}
		case tar.TypeReg:
			if err := claimExtractedPath(claim, dir, dest); err != nil {
				return err
			}
			if err := guard.addFile(); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return &output.InstallError{Err: err}
			}
			if err := writeFile(dest, tarReader, os.FileMode(header.Mode), guard); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			return &output.InstallError{Err: fmt.Errorf(
				"archive contains an unsupported symlink entry: %s", header.Name)}
		}
	}
}

// safeJoin joins base and name, returning an error if the result would escape
// base (zip-slip path traversal guard).
func safeJoin(base, name string) (string, error) {
	dest := filepath.Join(base, name)
	if !strings.HasPrefix(dest, filepath.Clean(base)+string(os.PathSeparator)) && dest != filepath.Clean(base) {
		return "", &output.InstallError{Err: fmt.Errorf("archive entry escapes target dir: %s", name)}
	}
	return dest, nil
}

// claimExtractedPath offers one entry's destination to the merge's claim hook as
// a path relative to the extraction directory, which is the identity two
// archives would collide on. A nil hook is the single-archive case, where there
// is nothing to collide with.
func claimExtractedPath(claim func(relative string) error, dir, dest string) error {
	if claim == nil {
		return nil
	}
	relative, err := filepath.Rel(filepath.Clean(dir), dest)
	if err != nil {
		return &output.InstallError{Err: err}
	}
	return claim(filepath.ToSlash(relative))
}

// writeFile writes reader into dest, enforcing the guard's total-size cap so a
// single entry cannot expand the archive past the guard's maximum.
func writeFile(dest string, reader io.Reader, mode os.FileMode, guard *extractGuard) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o200)
	if err != nil {
		return &output.InstallError{Err: err}
	}
	defer func() { _ = out.Close() }()
	remaining := guard.maxBytes - guard.bytes
	written, err := io.Copy(out, io.LimitReader(reader, remaining+1))
	if err != nil {
		return &output.InstallError{Err: err}
	}
	return guard.addBytes(written)
}

// archiveSlicedDiagnostics reports the manifest fields a sliced archive source
// ignores. A sliced addon's archives are named by its index, so `url` names no
// archive gpm downloads; the manifest still requires it for every archive
// source, so saying so is better than letting a stale URL look load-bearing.
func archiveSlicedDiagnostics(spec manifest.AddonSpec) []string {
	if spec.URL == "" {
		return nil
	}
	return []string{fmt.Sprintf(
		"addon %q is sliced, so its archives are named by %s and `url` (%s) is not downloaded",
		spec.Name, spec.Index, spec.URL)}
}
