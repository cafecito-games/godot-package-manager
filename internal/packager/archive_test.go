package packager

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

func TestWriteArchiveEmitsOneSortedEntryPerRegularFile(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(directory, "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "bin", "library.so"), []byte("binary"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "plugin.gd"), []byte("extends Node\n"), 0o644))

	tree, err := walkAddonTree(directory, ".")
	require.NoError(t, err)
	defer func() { require.NoError(t, tree.close()) }()

	archivePath := filepath.Join(t.TempDir(), "addon-core.zip")
	require.NoError(t, writeArchive(archivePath, []archiveFile{
		tree.archiveFileAt("plugin.gd"),
		tree.archiveFileAt("bin/library.so"),
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
	firstTree, err := walkAddonTree(directory, ".")
	require.NoError(t, err)
	require.NoError(t, writeArchive(first, []archiveFile{firstTree.archiveFileAt("plugin.gd")}))
	require.NoError(t, firstTree.close())

	require.NoError(t, os.Chtimes(source, time.Unix(1, 0), time.Unix(1, 0)))
	require.NoError(t, os.Chmod(source, 0o600))
	second := filepath.Join(t.TempDir(), "second.zip")
	secondTree, err := walkAddonTree(directory, ".")
	require.NoError(t, err)
	require.NoError(t, writeArchive(second, []archiveFile{secondTree.archiveFileAt("plugin.gd")}))
	require.NoError(t, secondTree.close())

	firstBytes, err := os.ReadFile(first)
	require.NoError(t, err)
	secondBytes, err := os.ReadFile(second)
	require.NoError(t, err)
	require.Equal(t, firstBytes, secondBytes)
}

func TestWriteArchiveLeavesNoArchiveBehindWhenASourceIsUnreadable(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "plugin.gd"), []byte("a"), 0o644))
	tree, err := walkAddonTree(directory, ".")
	require.NoError(t, err)
	defer func() { require.NoError(t, tree.close()) }()
	require.NoError(t, os.Remove(filepath.Join(directory, "plugin.gd")))

	archivePath := filepath.Join(t.TempDir(), "addon-core.zip")

	err = writeArchive(archivePath, []archiveFile{tree.archiveFileAt("plugin.gd")})
	require.Error(t, err)
	_, statErr := os.Stat(archivePath)
	require.True(t, os.IsNotExist(statErr))
	remaining, err := os.ReadDir(filepath.Dir(archivePath))
	require.NoError(t, err)
	require.Empty(t, remaining, "no temp file is left behind")
}

func TestWriteArchiveRefusesASourceReplacedByASymlink(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "plugin.gd")
	require.NoError(t, os.WriteFile(source, []byte("walked"), 0o644))
	tree, err := walkAddonTree(directory, ".")
	require.NoError(t, err)
	defer func() { require.NoError(t, tree.close()) }()

	outside := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	require.NoError(t, os.Remove(source))
	require.NoError(t, os.Symlink(outside, source))

	archivePath := filepath.Join(t.TempDir(), "addon-core.zip")
	err = writeArchive(archivePath, []archiveFile{tree.archiveFileAt("plugin.gd")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "plugin.gd")
	_, statErr := os.Stat(archivePath)
	require.True(t, os.IsNotExist(statErr))
}

func TestWriteArchiveRefusesASourceThatIsNoLongerTheWalkedFile(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "plugin.gd")
	require.NoError(t, os.WriteFile(source, []byte("walked"), 0o644))
	tree, err := walkAddonTree(directory, ".")
	require.NoError(t, err)
	defer func() { require.NoError(t, tree.close()) }()

	// A different file behind the same name, which is what a replacement that
	// stays inside the addon root produces: the open succeeds, but the bytes are
	// not the ones the walk accepted.
	require.NoError(t, os.Remove(source))
	require.NoError(t, os.WriteFile(source, []byte("substituted"), 0o644))

	archivePath := filepath.Join(t.TempDir(), "addon-core.zip")
	err = writeArchive(archivePath, []archiveFile{tree.archiveFileAt("plugin.gd")})
	require.Error(t, err)
	require.Equal(t, output.ExitManifest, output.CodeFor(err))
	require.Contains(t, err.Error(), "changed")
	_, statErr := os.Stat(archivePath)
	require.True(t, os.IsNotExist(statErr))
}

func TestMeasureArchiveHashesAndSizesTheSameFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "addon-core.zip")
	require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0o644))

	checksum, size, err := measureArchive(path)
	require.NoError(t, err)
	require.Equal(t, int64(10), size)

	digest := sha256.Sum256([]byte("0123456789"))
	require.Equal(t, hex.EncodeToString(digest[:]), checksum)
}
