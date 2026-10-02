package packager

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// treeFile is one regular file of the addon subtree.
type treeFile struct {
	// relativePath is the file's path relative to the addon root, with "/"
	// separators. It is the archive entry name, and for a .gdextension it is also
	// the index's section path key and the extensionPath handed to
	// PartitionExtension, so one spelling of a file's identity spans the tree
	// walk, the partition, the archives, and the index.
	relativePath string

	// executable records whether any execute bit is set on disk.
	executable bool
}

// addonTree is the addon subtree as packaging sees it: every regular file under
// the addon root, in ascending path order, and nothing else.
//
// It is the packager's only view of the tree, and it holds the addon root as an
// open *os.Root rather than as a path. Every read of a file goes through that
// handle, so no path this package hands to the operating system can resolve
// outside the addon root, which is what keeps a file the author never meant to
// publish out of a release archive.
type addonTree struct {
	// path is the addon root's filesystem path, used for diagnostics only.
	path string

	// root is the open addon root every file is read through.
	root *os.Root

	files []treeFile

	// positions maps a relative path to its index in files.
	positions map[string]int
}

// walkAddonTree collects the regular files under addonPath, which is a path
// relative to the addon repository's root, or "." for the root itself.
//
// The addon root is opened inside the repository root rather than by its own
// absolute path, so even a component of addonPath that becomes a symlink between
// its validation and this call cannot place the addon root outside the
// repository.
//
// A symlink anywhere in the subtree is rejected rather than followed or skipped.
// The installer already refuses symlinks in a fetched tree, so an archive
// carrying one would either be refused at install time or, if followed here,
// would smuggle a file from outside the addon into a slice. Anything that is
// neither a directory nor a regular file — a socket, a device, a named pipe — is
// refused for the same reason: it carries nothing a consumer can install, and
// guessing at it is worse than reporting it.
//
// An addon with no files is refused: there is nothing to publish, and an index
// naming empty archives is a stale config rather than a release.
//
// The caller owns closing the returned tree.
func walkAddonTree(repositoryRoot, addonPath string) (*addonTree, error) {
	root, err := openAddonRoot(repositoryRoot, addonPath)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(repositoryRoot, filepath.FromSlash(addonPath))
	tree := &addonTree{path: path, root: root, positions: map[string]int{}}
	if err := tree.walk(); err != nil {
		_ = root.Close()
		return nil, err
	}
	return tree, nil
}

// walk fills the tree from the open addon root.
func (tree *addonTree) walk() error {
	walkErr := fs.WalkDir(tree.root.FS(), ".", func(relativePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return manifestErrorf("reading the addon subtree at %s: %s", tree.describe(relativePath), err)
		}
		if relativePath == "." {
			// Checked explicitly rather than left to the walk: a root that is not
			// a directory would simply not be descended into, and the run would
			// report an addon with no files instead of what actually happened.
			if !entry.IsDir() {
				return manifestErrorf("the addon root %s is not a directory", tree.path)
			}
			return nil
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			return manifestErrorf(
				"%s is a symlink; a slice archive carries regular files only, so replace it with the file it points at",
				relativePath,
			)
		case entry.IsDir():
			return nil
		case !entry.Type().IsRegular():
			return manifestErrorf("%s is neither a regular file nor a directory, so it cannot be packaged", relativePath)
		}
		// Taken through the root handle rather than through the directory entry,
		// whose own Info lstats by path.
		info, err := tree.root.Lstat(relativePath)
		if err != nil {
			return manifestErrorf("reading %s: %s", relativePath, err)
		}
		if !info.Mode().IsRegular() {
			return manifestErrorf("%s is not a regular file, so it cannot be packaged", relativePath)
		}
		tree.files = append(tree.files, treeFile{
			relativePath: relativePath,
			executable:   info.Mode().Perm()&0o111 != 0,
		})
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	sort.Slice(tree.files, func(left, right int) bool {
		return tree.files[left].relativePath < tree.files[right].relativePath
	})
	for position, file := range tree.files {
		tree.positions[file.relativePath] = position
	}
	if len(tree.files) == 0 {
		return manifestErrorf("the addon subtree at %s holds no files, so there is nothing to package", tree.path)
	}
	return nil
}

// openAddonRoot opens the addon subtree as an *os.Root inside the repository
// root.
//
// Every component of addonPath is lstat-ed through the repository's own handle
// first, so a component that is a symlink is reported as such rather than
// followed, and a component that is not a directory is named in the diagnostic.
// The subtree itself is then opened through that same handle, which is what
// bounds every later read to the addon root.
//
// addonPath is already known to be a clean relative path, so it has no "." or
// ".." component; "." names the repository root itself, which is what the tests
// of this package use.
func openAddonRoot(repositoryRoot, addonPath string) (*os.Root, error) {
	repository, err := os.OpenRoot(repositoryRoot)
	if err != nil {
		return nil, manifestErrorf("opening the addon repository at %s: %s", repositoryRoot, err)
	}
	if addonPath == "." {
		return repository, nil
	}
	defer func() { _ = repository.Close() }()

	traversed := repositoryRoot
	prefix := ""
	for _, component := range strings.Split(addonPath, "/") {
		traversed = filepath.Join(traversed, component)
		prefix = path.Join(prefix, component)
		// Checked before anything deeper is addressed, so a symlinked component is
		// refused at itself rather than reported as a failure to resolve the rest
		// of the path.
		link, err := repository.Lstat(prefix)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, manifestErrorf("[package] addon_path %q names %s, which does not exist", addonPath, traversed)
			}
			return nil, manifestErrorf("[package] addon_path %q: reading %s: %s", addonPath, traversed, err)
		}
		if link.Mode()&os.ModeSymlink != 0 {
			return nil, manifestErrorf(
				"[package] addon_path %q passes through the symlink %s; the addon subtree is read as it sits in the repository",
				addonPath, traversed,
			)
		}
		if !link.IsDir() {
			return nil, manifestErrorf("[package] addon_path %q names %s, which is not a directory", addonPath, traversed)
		}
	}
	addonRoot, err := repository.OpenRoot(addonPath)
	if err != nil {
		return nil, manifestErrorf("[package] addon_path %q: opening %s: %s", addonPath, traversed, err)
	}
	return addonRoot, nil
}

// addonInfo returns the addon root's own file information, taken through the
// pinned root handle rather than by resolving its path again.
func (tree *addonTree) addonInfo() (fs.FileInfo, error) {
	info, err := tree.root.Stat(".")
	if err != nil {
		return nil, installErrorf("reading the addon subtree %s: %s", tree.path, err)
	}
	return info, nil
}

// close releases the addon root handle.
func (tree *addonTree) close() error { return tree.root.Close() }

// describe renders a path inside the addon root for a diagnostic.
func (tree *addonTree) describe(relativePath string) string {
	if relativePath == "." {
		return tree.path
	}
	return filepath.Join(tree.path, filepath.FromSlash(relativePath))
}

// paths returns every file's path relative to the addon root, in ascending
// order.
func (tree *addonTree) paths() []string {
	paths := make([]string, 0, len(tree.files))
	for _, file := range tree.files {
		paths = append(paths, file.relativePath)
	}
	return paths
}

// resolve returns the files a path names: the file itself, or, when the path is
// a directory, every file beneath it in ascending order. It returns nil for a
// path that names nothing.
//
// The directory case is not a convenience. A macOS .framework and an iOS
// .xcframework are directories, and a Godot [libraries] entry points straight at
// one, so resolving a bundle to its contents is what makes a real GDExtension
// addon packageable at all.
func (tree *addonTree) resolve(relativePath string) []string {
	if position, found := tree.positions[relativePath]; found {
		return []string{tree.files[position].relativePath}
	}
	prefix := relativePath + "/"
	var resolved []string
	for _, file := range tree.files {
		if strings.HasPrefix(file.relativePath, prefix) {
			resolved = append(resolved, file.relativePath)
		}
	}
	return resolved
}

// sourceAt returns the read handle for a walked file, which every caller reaches
// through resolve first.
func (tree *addonTree) sourceAt(relativePath string) fileSource {
	file := tree.files[tree.positions[relativePath]]
	return fileSource{
		root:        tree.root,
		name:        file.relativePath,
		displayPath: tree.describe(file.relativePath),
	}
}

// archiveFileAt returns the archive entry for a walked file.
func (tree *addonTree) archiveFileAt(relativePath string) archiveFile {
	file := tree.files[tree.positions[relativePath]]
	return archiveFile{
		archivePath: file.relativePath,
		source:      tree.sourceAt(relativePath),
		executable:  file.executable,
	}
}
