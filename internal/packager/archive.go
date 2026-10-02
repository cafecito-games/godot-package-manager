package packager

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// zipEpoch is the timestamp every archive entry carries: the MS-DOS zip epoch,
// which is the earliest instant the format represents without an extension.
//
// A source file's own modification time never reaches an archive. An addon's
// working tree is checked out, touched, and rebuilt, and none of that changes
// which bytes a consumer installs, so letting mtimes through would change an
// archive's checksum and invalidate a consumer's lock for no reason. It is a
// constant rather than a test seam for the same reason: there is no clock here
// to inject.
var zipEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// Archive entry permissions. Nothing else from a source file's mode — setuid,
// sticky, group and other bits, whatever the author's umask produced — reaches
// an archive, because none of it affects whether the installed addon works and
// all of it varies between the machines that cut a release.
const (
	executableArchiveMode os.FileMode = 0o755
	regularArchiveMode    os.FileMode = 0o644
)

// fileSource is a read handle for one file of the addon subtree: the open addon
// root, the file's name within it, and the file the tree walk accepted there.
//
// Reads go through the root rather than through a bare path, so a path cannot
// resolve outside the addon root however the tree changes under the packager.
type fileSource struct {
	root *os.Root
	name string

	// displayPath is the file's filesystem path, for diagnostics only.
	displayPath string

	// walkedInfo is the file the tree walk accepted, compared against the file
	// actually opened.
	walkedInfo fs.FileInfo
}

// archiveFile is one regular file going into a slice archive.
//
// Either source names a file of the addon subtree to copy, or content holds
// bytes the packager generated — which is how a .gdextension's partitioned core
// body reaches the core archive without being written back into the author's
// working tree.
type archiveFile struct {
	// archivePath is the entry name: the file's path relative to the addon root,
	// with "/" separators. Archives hold the addon subtree unprefixed, so a
	// consumer merges selected slices by extracting them into one root.
	archivePath string

	source  fileSource
	content []byte

	// executable records whether the source file had any execute bit set.
	executable bool
}

// writeArchive writes one reproducible zip archive at path.
//
// Two runs over an unchanged tree produce byte-identical archives, so re-running
// `gpm package` does not invalidate a consumer's lock. Five levers make that
// true rather than accidental:
//
//  1. Exactly one entry per regular file, and no directory entries. An empty
//     directory carries nothing to install, so it is not represented.
//  2. Entries sorted by their archive path, compared as a plain byte-wise string
//     sort over the "/"-separated name.
//  3. Every entry's Modified set to zipEpoch, and no other timestamp field set.
//  4. Permissions normalized to one of two modes.
//  5. The archive/zip default Deflate compressor, with no compressor registered,
//     so the output never depends on a compression level taken from a flag, an
//     environment variable, or a Go version's non-default settings.
//
// The write is atomic: the archive is built in a temp file beside path and
// renamed over it, so a failed run leaves no partial archive that a later upload
// could publish.
func writeArchive(path string, files []archiveFile) error {
	sorted := append([]archiveFile(nil), files...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left].archivePath < sorted[right].archivePath })

	temporary, err := os.CreateTemp(filepath.Dir(path), ".gpm-archive-*.tmp")
	if err != nil {
		return installErrorf("creating temp archive for %s: %s", path, err)
	}
	temporaryName := temporary.Name()
	discard := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}

	writer := zip.NewWriter(temporary)
	for _, file := range sorted {
		if err := writeArchiveEntry(writer, file); err != nil {
			discard()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		discard()
		return installErrorf("finishing archive %s: %s", path, err)
	}
	if err := temporary.Sync(); err != nil {
		discard()
		return installErrorf("syncing archive %s: %s", path, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryName)
		return installErrorf("closing archive %s: %s", path, err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		_ = os.Remove(temporaryName)
		return installErrorf("installing archive %s: %s", path, err)
	}
	return nil
}

// writeArchiveEntry writes one file's entry with the normalized header.
func writeArchiveEntry(writer *zip.Writer, file archiveFile) error {
	mode := regularArchiveMode
	if file.executable {
		mode = executableArchiveMode
	}
	header := &zip.FileHeader{Name: file.archivePath, Method: zip.Deflate}
	header.Modified = zipEpoch
	header.SetMode(mode)

	entry, err := writer.CreateHeader(header)
	if err != nil {
		return installErrorf("adding %s to the archive: %s", file.archivePath, err)
	}
	if file.source.root == nil {
		if _, err := entry.Write(file.content); err != nil {
			return installErrorf("writing %s into the archive: %s", file.archivePath, err)
		}
		return nil
	}
	source, err := openRegularFile(file.source)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	if _, err := io.Copy(entry, source); err != nil {
		return installErrorf("writing %s into the archive: %s", file.archivePath, err)
	}
	return nil
}

// openRegularFile opens a file of the addon subtree and refuses anything that is
// not the regular file the tree walk accepted.
//
// Two rules apply, and they do different amounts of work. The open goes through
// the addon root's handle, so every component is resolved inside that root and a
// directory replaced by a symlink after the walk cannot make the path reach
// outside it; that part is a guarantee. The identity comparison beside it is
// best effort: it reports a file that no longer has the device and inode the
// walk recorded, which catches an addon tree edited while it was being packaged
// often enough to be worth the two syscalls, but it is not a defense against a
// deliberate swap. A replacement at the same path can be allocated the inode the
// original just freed — Linux routinely does — and then the comparison succeeds.
// The guarantee here is containment, not freshness.
func openRegularFile(source fileSource) (*os.File, error) {
	file, err := source.root.Open(source.name)
	if err != nil {
		return nil, installErrorf("reading %s: %s", source.displayPath, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, installErrorf("reading %s: %s", source.displayPath, err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, manifestErrorf(
			"%s is not a regular file; a slice archive carries regular files only", source.displayPath,
		)
	}
	if source.walkedInfo != nil && !os.SameFile(source.walkedInfo, info) {
		_ = file.Close()
		return nil, manifestErrorf(
			"%s changed while the addon was being packaged; it is no longer the file the tree walk accepted",
			source.displayPath,
		)
	}
	return file, nil
}

// readRegularFile reads a file through the same symlink guard an archive source
// goes through, for content the packager reads rather than copies.
func readRegularFile(source fileSource) ([]byte, error) {
	file, err := openRegularFile(source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, installErrorf("reading %s: %s", source.displayPath, err)
	}
	return content, nil
}

// measureArchive returns an archive's SHA-256 digest as lowercase hex and its
// size in bytes, both read back from the file on disk after it was written.
//
// They are never estimated while writing: the index pins what a consumer will
// download, so the only bytes worth hashing are the bytes that actually landed.
func measureArchive(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, installErrorf("reading archive %s: %s", path, err)
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return "", 0, installErrorf("hashing archive %s: %s", path, err)
	}
	// Taken from the descriptor the bytes were read from rather than by stat-ing
	// the path again: the index pins a digest and a size that must describe one
	// archive, and a second resolution of the path could measure a different file
	// of the same length.
	info, err := file.Stat()
	if err != nil {
		return "", 0, installErrorf("measuring archive %s: %s", path, err)
	}
	if info.Size() != size {
		return "", 0, installErrorf(
			"archive %s changed while it was hashed: %d bytes hashed, %d bytes in the file", path, size, info.Size(),
		)
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}
