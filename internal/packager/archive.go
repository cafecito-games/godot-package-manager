package packager

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"io"
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

// archiveFile is one regular file going into a slice archive.
//
// Either sourcePath names a file to copy, or content holds bytes the packager
// generated — which is how a .gdextension's partitioned core body reaches the
// core archive without being written back into the author's working tree.
type archiveFile struct {
	// archivePath is the entry name: the file's path relative to the addon root,
	// with "/" separators. Archives hold the addon subtree unprefixed, so a
	// consumer merges selected slices by extracting them into one root.
	archivePath string

	sourcePath string
	content    []byte

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
	if file.sourcePath == "" {
		if _, err := entry.Write(file.content); err != nil {
			return installErrorf("writing %s into the archive: %s", file.archivePath, err)
		}
		return nil
	}
	source, err := openRegularFile(file.sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	if _, err := io.Copy(entry, source); err != nil {
		return installErrorf("writing %s into the archive: %s", file.archivePath, err)
	}
	return nil
}

// openRegularFile opens an archive source and refuses anything that is not a
// regular file.
//
// The tree walk already rejected every symlink and every irregular file it saw,
// but it recorded paths rather than open file handles, so a path accepted then
// is re-resolved here. Re-checking closes that gap: without it, a regular file
// replaced by a symlink between the walk and the archive write would publish
// whatever the link points at, which may be any readable file outside the addon
// root. The open flag refuses the link where the platform has one, and the check
// on the opened file refuses it everywhere.
func openRegularFile(path string) (*os.File, error) {
	// Reported as a manifest failure, matching what the tree walk says about a
	// symlink: whatever changed under the packager, a link in the subtree is the
	// author's tree to fix.
	link, err := os.Lstat(path)
	if err != nil {
		return nil, installErrorf("reading %s: %s", path, err)
	}
	if !link.Mode().IsRegular() {
		return nil, manifestErrorf(
			"%s is not a regular file; a slice archive carries regular files only", path,
		)
	}
	file, err := os.OpenFile(path, os.O_RDONLY|openNoFollow, 0)
	if err != nil {
		return nil, installErrorf("reading %s: %s", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, installErrorf("reading %s: %s", path, err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, manifestErrorf(
			"%s is no longer a regular file; a slice archive carries regular files only", path,
		)
	}
	return file, nil
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
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, installErrorf("measuring archive %s: %s", path, err)
	}
	if info.Size() != size {
		return "", 0, installErrorf(
			"archive %s changed while it was hashed: %d bytes hashed, %d bytes on disk", path, size, info.Size(),
		)
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}

// readRegularFile reads a file through the same symlink guard an archive source
// goes through, for content the packager reads rather than copies.
func readRegularFile(path string) ([]byte, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, installErrorf("reading %s: %s", path, err)
	}
	return content, nil
}
