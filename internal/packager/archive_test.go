package packager

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteArchiveEmitsOneSortedEntryPerRegularFile(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(directory, "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "bin", "library.so"), []byte("binary"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "plugin.gd"), []byte("extends Node\n"), 0o644))

	archivePath := filepath.Join(t.TempDir(), "addon-core.zip")
	require.NoError(t, writeArchive(archivePath, []archiveFile{
		{archivePath: "plugin.gd", sourcePath: filepath.Join(directory, "plugin.gd")},
		{archivePath: "bin/library.so", sourcePath: filepath.Join(directory, "bin", "library.so"), executable: true},
		{archivePath: "generated.gdextension", content: []byte("[configuration]\n")},
	}))

	reader, err := zip.OpenReader(archivePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()

	names := make([]string, 0, len(reader.File))
	for _, entry := range reader.File {
		names = append(names, entry.Name)
		require.False(t, entry.FileInfo().IsDir(), "no directory entries")
		require.Equal(t, time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC), entry.Modified.UTC())
		require.Equal(t, uint16(zip.Deflate), entry.Method)
	}
	require.Equal(t, []string{"bin/library.so", "generated.gdextension", "plugin.gd"}, names)
	require.Equal(t, os.FileMode(0o755), reader.File[0].Mode().Perm())
	require.Equal(t, os.FileMode(0o644), reader.File[1].Mode().Perm())
	require.Equal(t, os.FileMode(0o644), reader.File[2].Mode().Perm())
}

func TestWriteArchiveIgnoresSourceTimestampsAndSurplusModeBits(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "plugin.gd")
	require.NoError(t, os.WriteFile(source, []byte("extends Node\n"), 0o644))

	first := filepath.Join(t.TempDir(), "first.zip")
	require.NoError(t, writeArchive(first, []archiveFile{{archivePath: "plugin.gd", sourcePath: source}}))

	require.NoError(t, os.Chtimes(source, time.Unix(1, 0), time.Unix(1, 0)))
	require.NoError(t, os.Chmod(source, 0o600))
	second := filepath.Join(t.TempDir(), "second.zip")
	require.NoError(t, writeArchive(second, []archiveFile{{archivePath: "plugin.gd", sourcePath: source}}))

	firstBytes, err := os.ReadFile(first)
	require.NoError(t, err)
	secondBytes, err := os.ReadFile(second)
	require.NoError(t, err)
	require.Equal(t, firstBytes, secondBytes)
}

func TestWriteArchiveLeavesNoArchiveBehindWhenASourceIsUnreadable(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "addon-core.zip")

	err := writeArchive(archivePath, []archiveFile{
		{archivePath: "missing.gd", sourcePath: filepath.Join(t.TempDir(), "missing.gd")},
	})
	require.Error(t, err)
	_, statErr := os.Stat(archivePath)
	require.True(t, os.IsNotExist(statErr))
	remaining, err := os.ReadDir(filepath.Dir(archivePath))
	require.NoError(t, err)
	require.Empty(t, remaining, "no temp file is left behind")
}
