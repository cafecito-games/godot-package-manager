package source

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestArchiveFetchExtractsZip(t *testing.T) {
	payload := zipBytes(t, map[string]string{"addons/x/plugin.cfg": "[plugin]"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	f := &ArchiveFetcher{}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceArchive, URL: srv.URL + "/x.zip",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	require.NotEmpty(t, res.Checksum)
	got, err := os.ReadFile(filepath.Join(res.Dir, "addons", "x", "plugin.cfg"))
	require.NoError(t, err)
	require.Equal(t, "[plugin]", string(got))
}

func tarGzBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, body := range files {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		require.NoError(t, tarWriter.WriteHeader(header))
		_, err := tarWriter.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	return buf.Bytes()
}

func TestArchiveFetchExtractsTarGz(t *testing.T) {
	payload := tarGzBytes(t, map[string]string{"addons/y/plugin.cfg": "[plugin]"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	f := &ArchiveFetcher{}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceArchive, URL: srv.URL + "/x.tar.gz",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	require.NotEmpty(t, res.Checksum)
	got, err := os.ReadFile(filepath.Join(res.Dir, "addons", "y", "plugin.cfg"))
	require.NoError(t, err)
	require.Equal(t, "[plugin]", string(got))
}

func TestArchiveFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	f := &ArchiveFetcher{}
	_, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceArchive, URL: srv.URL + "/missing.zip",
	})
	require.Error(t, err)
	var fetchErr *output.FetchError
	require.ErrorAs(t, err, &fetchErr)
}

func TestArchiveFetchDetectsArchiveTypeFromURLPathWithQuery(t *testing.T) {
	payload := zipBytes(t, map[string]string{"plugin.cfg": "[plugin]"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	f := &ArchiveFetcher{}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceArchive, URL: srv.URL + "/ADDON.ZIP?token=secret",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	got, err := os.ReadFile(filepath.Join(res.Dir, "plugin.cfg"))
	require.NoError(t, err)
	require.Equal(t, "[plugin]", string(got))
}

func TestDownloadRejectsOversizedResponse(t *testing.T) {
	oldMax := maxDownloadBytes
	maxDownloadBytes = 4
	defer func() { maxDownloadBytes = oldMax }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("12345"))
	}))
	defer srv.Close()

	_, err := download(context.Background(), srv.URL, nil)
	require.Error(t, err)
	var fetchErr *output.FetchError
	require.ErrorAs(t, err, &fetchErr)
	require.Contains(t, err.Error(), "exceeds maximum download size")
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	base := t.TempDir()

	_, err := safeJoin(base, "../escape")
	require.Error(t, err)

	dest, err := safeJoin(base, "ok/file.txt")
	require.NoError(t, err)
	require.True(t, len(dest) > len(base), "result should be inside base")
}
