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
func (f *ArchiveFetcher) Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error) {
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

// extractArchive extracts the archive at archivePath into dir, choosing zip vs
// tar.gz based on nameHint's file extension. maxExtracted <= 0 uses the
// package default cap.
func extractArchive(nameHint, archivePath, dir string, maxExtracted int64) error {
	archiveName := archiveNameForDetection(nameHint)
	switch {
	case strings.HasSuffix(archiveName, ".zip"):
		return extractZip(archivePath, dir, maxExtracted)
	case strings.HasSuffix(archiveName, ".tar.gz"), strings.HasSuffix(archiveName, ".tgz"):
		return extractTarGz(archivePath, dir, maxExtracted)
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

// extractGuard enforces per-archive limits on entry count and total
// uncompressed size.
type extractGuard struct {
	files    int
	bytes    int64
	maxBytes int64
}

func newExtractGuard(maxBytes int64) *extractGuard {
	if maxBytes <= 0 {
		maxBytes = maxExtractedBytes
	}
	return &extractGuard{maxBytes: maxBytes}
}

func (g *extractGuard) addFile() error {
	g.files++
	if g.files > maxExtractedFiles {
		return &output.InstallError{Err: fmt.Errorf(
			"archive contains more than %d entries", maxExtractedFiles)}
	}
	return nil
}

func (g *extractGuard) addBytes(n int64) error {
	g.bytes += n
	if g.bytes > g.maxBytes {
		return &output.InstallError{Err: fmt.Errorf(
			"archive expands beyond the maximum extracted size of %d bytes", g.maxBytes)}
	}
	return nil
}

func extractZip(archivePath, dir string, maxExtracted int64) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return &output.InstallError{Err: err}
	}
	defer func() { _ = reader.Close() }()

	guard := newExtractGuard(maxExtracted)
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

func extractTarGz(archivePath, dir string, maxExtracted int64) error {
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

	guard := newExtractGuard(maxExtracted)
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
