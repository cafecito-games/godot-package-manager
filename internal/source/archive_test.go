package source

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
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
}
