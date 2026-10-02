package packager

import (
	"io/fs"
	"os"
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

	// sourcePath is the file's absolute path on disk.
	sourcePath string

	// executable records whether any execute bit is set on disk.
	executable bool
}

// addonTree is the addon subtree as packaging sees it: every regular file under
// the addon root, in ascending path order, and nothing else.
//
// It is the packager's only view of the tree. Every later step — partitioning,
// claiming the files an entry names, matching an extras glob, archive
// membership — resolves a path through this walk rather than stat-ing the disk
// again, which is what guarantees that no archive can hold a file from outside
// the addon root or a file reached through a symlink.
type addonTree struct {
	root  string
	files []treeFile

	// positions maps a relative path to its index in files.
	positions map[string]int
}

// walkAddonTree collects the regular files under an addon root.
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
func walkAddonTree(root string) (*addonTree, error) {
	tree := &addonTree{root: root, positions: map[string]int{}}
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return manifestErrorf("reading the addon subtree at %s: %s", path, err)
		}
		if path == root {
			return nil
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return manifestErrorf("locating %s under the addon root: %s", path, err)
		}
		relativePath = filepath.ToSlash(relativePath)
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
		info, err := entry.Info()
		if err != nil {
			return manifestErrorf("reading %s: %s", relativePath, err)
		}
		tree.files = append(tree.files, treeFile{
			relativePath: relativePath,
			sourcePath:   path,
			executable:   info.Mode().Perm()&0o111 != 0,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(tree.files, func(left, right int) bool {
		return tree.files[left].relativePath < tree.files[right].relativePath
	})
	for position, file := range tree.files {
		tree.positions[file.relativePath] = position
	}
	if len(tree.files) == 0 {
		return nil, manifestErrorf("the addon subtree at %s holds no files, so there is nothing to package", root)
	}
	return tree, nil
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

// fileAt returns the walked file at a relative path, which every caller reaches
// through resolve first.
func (tree *addonTree) fileAt(relativePath string) treeFile {
	return tree.files[tree.positions[relativePath]]
}
