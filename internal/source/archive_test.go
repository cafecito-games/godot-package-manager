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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("12345"))
	}))
	defer srv.Close()

	_, err := download(context.Background(), nil, srv.URL, nil, 4)
	require.Error(t, err)
	var fetchErr *output.FetchError
	require.ErrorAs(t, err, &fetchErr)
	require.Contains(t, err.Error(), "exceeds maximum download size")
}

func TestDownloadToFileRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("12345"))
	}))
	defer srv.Close()

	_, _, err := downloadToFile(context.Background(), nil, srv.URL, nil, 4)
	require.Error(t, err)
	var fetchErr *output.FetchError
	require.ErrorAs(t, err, &fetchErr)
	require.Contains(t, err.Error(), "exceeds maximum download size")
}

func TestArchiveFetchRejectsDecompressionBomb(t *testing.T) {
	// Lower the cap so the test stays fast; the production value is far larger.
	originalCap := maxExtractedBytes
	maxExtractedBytes = 1 << 20
	defer func() { maxExtractedBytes = originalCap }()

	// A zip whose single entry decompresses to more than maxExtractedBytes.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("bomb.bin")
	require.NoError(t, err)
	chunk := make([]byte, 64<<10) // highly compressible zeros
	for written := int64(0); written <= maxExtractedBytes; written += int64(len(chunk)) {
		_, err = w.Write(chunk)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	f := &ArchiveFetcher{}
	_, err = f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceArchive, URL: srv.URL + "/bomb.zip",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "maximum extracted size")
}

func TestArchiveFetchHonorsPerFetcherMaxBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), 1024))
	}))
	defer srv.Close()

	f := &ArchiveFetcher{maxBytes: 16}
	_, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceArchive, URL: srv.URL + "/big.zip",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum download size")
}

func TestArchiveFetchHonorsPerFetcherMaxExtracted(t *testing.T) {
	payload := zipBytes(t, map[string]string{"big.bin": string(bytes.Repeat([]byte("a"), 2048))})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	f := &ArchiveFetcher{maxExtracted: 128}
	_, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceArchive, URL: srv.URL + "/big.zip",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "maximum extracted size")
}

func TestArchiveExtractRejectsSymlinkEntry(t *testing.T) {
	cases := map[string]struct {
		tarEntries []tar.Header
		zipLinks   []struct {
			name   string
			target string
		}
		zipFiles map[string]string
		wantText string
	}{
		"absolute target": {
			tarEntries: []tar.Header{
				{Name: "evil-link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777},
			},
			zipLinks: []struct{ name, target string }{
				{name: "evil-link", target: "/etc/passwd"},
			},
			wantText: "symlink",
		},
		"parent traversal escape": {
			tarEntries: []tar.Header{
				{Name: "nested/evil", Typeflag: tar.TypeSymlink, Linkname: "../../passwd", Mode: 0o777},
			},
			zipLinks: []struct{ name, target string }{
				{name: "nested/evil", target: "../../passwd"},
			},
			wantText: "symlink",
		},
		"chained escape across two individually contained links": {
			tarEntries: []tar.Header{
				{Name: "a/b/up", Typeflag: tar.TypeSymlink, Linkname: "../..", Mode: 0o777},
				{Name: "a/b/escape", Typeflag: tar.TypeSymlink, Linkname: "up/../secret", Mode: 0o777},
			},
			zipLinks: []struct{ name, target string }{
				{name: "a/b/up", target: "../.."},
				{name: "a/b/escape", target: "up/../secret"},
			},
			wantText: "symlink",
		},
		"escape via materialized parent link": {
			tarEntries: []tar.Header{
				{Name: "a/b/parent", Typeflag: tar.TypeSymlink, Linkname: "../..", Mode: 0o777},
				{Name: "a/b/parent/escape", Typeflag: tar.TypeSymlink, Linkname: "../secret", Mode: 0o777},
			},
			zipLinks: []struct{ name, target string }{
				{name: "a/b/parent", target: "../.."},
				{name: "a/b/parent/escape", target: "../secret"},
			},
			wantText: "symlink",
		},
		"regular file beneath symlinked parent": {
			tarEntries: []tar.Header{
				{Name: "a/parent", Typeflag: tar.TypeSymlink, Linkname: "..", Mode: 0o777},
			},
			zipLinks: []struct{ name, target string }{
				{name: "a/parent", target: ".."},
			},
			zipFiles: map[string]string{"a/parent/file.txt": "x"},
			wantText: "symlink",
		},
		"hard link entry": {
			tarEntries: []tar.Header{
				{Name: "real.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
				{Name: "hard-link", Typeflag: tar.TypeLink, Linkname: "real.txt", Mode: 0o644},
			},
			wantText: "hard link",
		},
	}

	for name, testCase := range cases {
		t.Run(name+"/tar.gz", func(t *testing.T) {
			var buffer bytes.Buffer
			gzipWriter := gzip.NewWriter(&buffer)
			tarWriter := tar.NewWriter(gzipWriter)
			for _, header := range testCase.tarEntries {
				headerCopy := header
				require.NoError(t, tarWriter.WriteHeader(&headerCopy))
				if headerCopy.Typeflag == tar.TypeReg && headerCopy.Size > 0 {
					_, err := tarWriter.Write(bytes.Repeat([]byte("x"), int(headerCopy.Size)))
					require.NoError(t, err)
				}
			}
			for fileName, content := range testCase.zipFiles {
				require.NoError(t, tarWriter.WriteHeader(&tar.Header{
					Name: fileName, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content)),
				}))
				_, err := tarWriter.Write([]byte(content))
				require.NoError(t, err)
			}
			require.NoError(t, tarWriter.Close())
			require.NoError(t, gzipWriter.Close())

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Write(buffer.Bytes())
			}))
			defer server.Close()

			fetcher := &ArchiveFetcher{}
			_, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
				Source: manifest.SourceArchive, URL: server.URL + "/x.tar.gz",
			})
			require.Error(t, err)
			var installErr *output.InstallError
			require.ErrorAs(t, err, &installErr)
			require.Contains(t, err.Error(), testCase.wantText)
		})

		if len(testCase.zipLinks) == 0 {
			continue
		}

		t.Run(name+"/zip", func(t *testing.T) {
			var buffer bytes.Buffer
			zipWriter := zip.NewWriter(&buffer)
			for _, link := range testCase.zipLinks {
				header := &zip.FileHeader{Name: link.name, Method: zip.Deflate}
				header.SetMode(os.ModeSymlink | 0o777)
				entry, err := zipWriter.CreateHeader(header)
				require.NoError(t, err)
				_, err = entry.Write([]byte(link.target))
				require.NoError(t, err)
			}
			for fileName, content := range testCase.zipFiles {
				entry, err := zipWriter.Create(fileName)
				require.NoError(t, err)
				_, err = entry.Write([]byte(content))
				require.NoError(t, err)
			}
			require.NoError(t, zipWriter.Close())

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Write(buffer.Bytes())
			}))
			defer server.Close()

			fetcher := &ArchiveFetcher{}
			_, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
				Source: manifest.SourceArchive, URL: server.URL + "/x.zip",
			})
			require.Error(t, err)
			var installErr *output.InstallError
			require.ErrorAs(t, err, &installErr)
			require.Contains(t, err.Error(), testCase.wantText)
		})
	}
}

func TestArchiveExtractMaterializesContainedSymlinks(t *testing.T) {
	links := []struct{ name, target string }{
		// Deliberately emit links before the target files exist to prove
		// dangling/out-of-order entries work cleanly.
		{name: "SwiftGodot.framework/SwiftGodot", target: "Versions/Current/SwiftGodot"},
		{name: "SwiftGodot.framework/Resources", target: "Versions/Current/Resources"},
		{name: "SwiftGodot.framework/Headers", target: "Versions/Current/Headers"},
		{name: "SwiftGodot.framework/Versions/Current", target: "A"},
	}

	t.Run("zip", func(t *testing.T) {
		var buffer bytes.Buffer
		zipWriter := zip.NewWriter(&buffer)
		for _, link := range links {
			header := &zip.FileHeader{Name: link.name, Method: zip.Deflate}
			header.SetMode(os.ModeSymlink | 0o777)
			entry, err := zipWriter.CreateHeader(header)
			require.NoError(t, err)
			_, err = entry.Write([]byte(link.target))
			require.NoError(t, err)
		}
		entry, err := zipWriter.Create("SwiftGodot.framework/Versions/A/SwiftGodot")
		require.NoError(t, err)
		_, err = entry.Write([]byte("macho-binary"))
		require.NoError(t, err)
		require.NoError(t, zipWriter.Close())

		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Write(buffer.Bytes())
		}))
		defer server.Close()

		fetcher := &ArchiveFetcher{}
		result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
			Source: manifest.SourceArchive, URL: server.URL + "/fw.zip",
		})
		require.NoError(t, err)
		defer os.RemoveAll(result.Dir)

		for _, link := range links {
			linkPath := filepath.Join(result.Dir, filepath.FromSlash(link.name))
			info, err := os.Lstat(linkPath)
			require.NoError(t, err)
			require.NotZero(t, info.Mode()&os.ModeSymlink)
			gotTarget, err := os.Readlink(linkPath)
			require.NoError(t, err)
			require.Equal(t, filepath.FromSlash(link.target), gotTarget)
		}
		resolvedContent, err := os.ReadFile(filepath.Join(result.Dir, "SwiftGodot.framework", "SwiftGodot"))
		require.NoError(t, err)
		require.Equal(t, "macho-binary", string(resolvedContent))
	})

	t.Run("tar.gz", func(t *testing.T) {
		var buffer bytes.Buffer
		gzipWriter := gzip.NewWriter(&buffer)
		tarWriter := tar.NewWriter(gzipWriter)
		for _, link := range links {
			require.NoError(t, tarWriter.WriteHeader(&tar.Header{
				Name: "./" + link.name, Typeflag: tar.TypeSymlink, Linkname: link.target, Mode: 0o777,
			}))
		}
		require.NoError(t, tarWriter.WriteHeader(&tar.Header{
			Name: "./", Typeflag: tar.TypeDir, Mode: 0o755,
		}))
		binaryBytes := []byte("macho-binary")
		require.NoError(t, tarWriter.WriteHeader(&tar.Header{
			Name: "SwiftGodot.framework/Versions/A/SwiftGodot", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(binaryBytes)),
		}))
		_, err := tarWriter.Write(binaryBytes)
		require.NoError(t, err)
		require.NoError(t, tarWriter.Close())
		require.NoError(t, gzipWriter.Close())

		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Write(buffer.Bytes())
		}))
		defer server.Close()

		fetcher := &ArchiveFetcher{}
		result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
			Source: manifest.SourceArchive, URL: server.URL + "/fw.tar.gz",
		})
		require.NoError(t, err)
		defer os.RemoveAll(result.Dir)

		for _, link := range links {
			linkPath := filepath.Join(result.Dir, filepath.FromSlash(link.name))
			info, err := os.Lstat(linkPath)
			require.NoError(t, err)
			require.NotZero(t, info.Mode()&os.ModeSymlink)
			gotTarget, err := os.Readlink(linkPath)
			require.NoError(t, err)
			require.Equal(t, filepath.FromSlash(link.target), gotTarget)
		}
		resolvedContent, err := os.ReadFile(filepath.Join(result.Dir, "SwiftGodot.framework", "SwiftGodot"))
		require.NoError(t, err)
		require.Equal(t, "macho-binary", string(resolvedContent))
	})
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	base := t.TempDir()

	_, err := safeJoin(base, "../escape")
	require.Error(t, err)

	dest, err := safeJoin(base, "ok/file.txt")
	require.NoError(t, err)
	require.True(t, len(dest) > len(base), "result should be inside base")
}
