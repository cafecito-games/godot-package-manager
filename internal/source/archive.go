package source

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// ArchiveFetcher downloads and extracts a plain zip or tarball URL.
type ArchiveFetcher struct{}

// Fetch downloads spec.URL, extracts it into a new temp directory, and reports
// the archive's SHA-256 checksum.
func (f *ArchiveFetcher) Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error) {
	data, err := download(ctx, spec.URL, nil)
	if err != nil {
		return FetchResult{}, err
	}
	dir, err := os.MkdirTemp("", "gpm-archive-*")
	if err != nil {
		return FetchResult{}, &output.FetchError{Err: err}
	}
	if err := extractArchive(spec.URL, data, dir); err != nil {
		_ = os.RemoveAll(dir)
		return FetchResult{}, err
	}
	sum := sha256.Sum256(data)
	return FetchResult{
		Dir:             dir,
		ResolvedVersion: spec.Version,
		Checksum:        hex.EncodeToString(sum[:]),
	}, nil
}

// download performs an HTTP GET and returns the response body. header, if
// non-nil, is applied to the request (used by the GitHub release fetcher for
// auth headers).
func download(ctx context.Context, url string, header http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &output.FetchError{Err: err}
	}
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, &output.FetchError{Err: fmt.Errorf("downloading %s: %w", url, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &output.FetchError{Err: fmt.Errorf("downloading %s: HTTP %d", url, resp.StatusCode)}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &output.FetchError{Err: err}
	}
	return body, nil
}

// extractArchive extracts data into dir, choosing zip vs tar.gz based on
// nameHint's file extension.
func extractArchive(nameHint string, data []byte, dir string) error {
	switch {
	case strings.HasSuffix(nameHint, ".zip"):
		return extractZip(data, dir)
	case strings.HasSuffix(nameHint, ".tar.gz"), strings.HasSuffix(nameHint, ".tgz"):
		return extractTarGz(data, dir)
	default:
		return &output.FetchError{Err: fmt.Errorf("unsupported archive type: %s", nameHint)}
	}
}

func extractZip(data []byte, dir string) error {
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return &output.InstallError{Err: err}
	}
	for _, zipFile := range zipReader.File {
		dest, err := safeJoin(dir, zipFile.Name)
		if err != nil {
			return err
		}
		if zipFile.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return &output.InstallError{Err: err}
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return &output.InstallError{Err: err}
		}
		readCloser, err := zipFile.Open()
		if err != nil {
			return &output.InstallError{Err: err}
		}
		err = writeFile(dest, readCloser, zipFile.Mode())
		_ = readCloser.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(data []byte, dir string) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
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
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return &output.InstallError{Err: err}
			}
			if err := writeFile(dest, tarReader, os.FileMode(header.Mode)); err != nil {
				return err
			}
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

func writeFile(dest string, reader io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o200)
	if err != nil {
		return &output.InstallError{Err: err}
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, reader); err != nil {
		return &output.InstallError{Err: err}
	}
	return nil
}
