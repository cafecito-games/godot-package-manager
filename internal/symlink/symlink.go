package symlink

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// maxSymlinkHops caps total symlink expansions when resolving one symlink,
// matching POSIX/Linux MAXSYMLINKS so pathological non-cyclic DAGs cannot take
// exponential steps.
const maxSymlinkHops = 40

// Entry is one validated relative symlink inside a tree root, with both Path
// and Target expressed using "/" separators.
type Entry struct {
	Path   string
	Target string
}

// Error describes why one symlink entry or path in a tree was rejected.
type Error struct {
	Path   string
	Target string
	Reason string
}

func (e *Error) Error() string {
	if e.Target != "" {
		return fmt.Sprintf("%s -> %q: %s", e.Path, e.Target, e.Reason)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Reason)
}

type occupiedEntry struct {
	spelling    string
	isDirectory bool
}

type resolutionFrame struct {
	linkKey   string
	remaining []string
}

// Tree validates a set of regular files, directories, and relative symlinks
// rooted at one directory before any symlink is created on disk.
type Tree struct {
	occupied   map[string]occupiedEntry
	linksByKey map[string]Entry
	links      []Entry
}

// NewTree returns an empty lexical containment tree.
func NewTree() *Tree {
	return &Tree{
		occupied:   map[string]occupiedEntry{},
		linksByKey: map[string]Entry{},
	}
}

// NormalizeFilesystemTarget converts a target read from the local filesystem
// via os.Readlink into a slash-separated relative target, rejecting targets
// that are empty, contain NUL bytes, or are absolute on Unix or Windows.
func NormalizeFilesystemTarget(rawTarget string) (string, error) {
	if err := checkRawTargetPrefix(rawTarget); err != nil {
		return "", err
	}
	normalized := rawTarget
	if runtime.GOOS == "windows" {
		normalized = filepath.ToSlash(rawTarget)
	}
	if err := ValidateTarget(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

// ValidateTarget checks that target is a relative, slash-separated symlink
// target with no NUL bytes, backslashes, or volume/root prefixes.
func ValidateTarget(target string) error {
	if err := checkRawTargetPrefix(target); err != nil {
		return err
	}
	if strings.ContainsRune(target, '\\') {
		return fmt.Errorf("symlink target %q must use '/' separators rather than backslashes", target)
	}
	return nil
}

func checkRawTargetPrefix(target string) error {
	if target == "" {
		return fmt.Errorf("symlink target must not be empty")
	}
	if strings.ContainsRune(target, 0) {
		return fmt.Errorf("symlink target contains a NUL byte")
	}
	if strings.HasPrefix(target, "/") || strings.HasPrefix(target, `\`) || filepath.IsAbs(target) || hasDrivePrefix(target) {
		return fmt.Errorf("symlink target %q must be relative, not absolute", target)
	}
	return nil
}

// ValidateRelativeTarget checks that linkPath is a clean relative entry path
// inside the root and that target is a relative symlink target that does not
// step above the root on its own.
func ValidateRelativeTarget(linkPath, target string) error {
	cleanedPath, err := cleanRelativeEntryPath(linkPath)
	if err != nil {
		return &Error{Path: linkPath, Target: target, Reason: err.Error()}
	}
	if err := ValidateTarget(target); err != nil {
		return &Error{Path: cleanedPath, Target: target, Reason: err.Error()}
	}
	currentDirectory := parentComponents(cleanedPath)
	for _, component := range strings.Split(target, "/") {
		switch component {
		case "", ".":
			continue
		case "..":
			if len(currentDirectory) == 0 {
				return &Error{Path: cleanedPath, Target: target, Reason: "target escapes the install root"}
			}
			currentDirectory = currentDirectory[:len(currentDirectory)-1]
		default:
			currentDirectory = append(currentDirectory, component)
		}
	}
	return nil
}

// AddFile records a regular file path and all of its ancestor directories.
func (t *Tree) AddFile(relativePath string) error {
	cleaned, err := cleanOccupiedPath(relativePath)
	if err != nil {
		return &Error{Path: relativePath, Reason: err.Error()}
	}
	if err := t.recordAncestors(cleaned); err != nil {
		return err
	}
	key := foldKey(cleaned)
	if existing, exists := t.occupied[key]; exists && existing.isDirectory {
		return &Error{Path: cleaned, Reason: fmt.Sprintf("file path conflicts with directory %q", existing.spelling)}
	}
	t.occupied[key] = occupiedEntry{spelling: cleaned, isDirectory: false}
	return nil
}

// AddDirectory records a directory path and all of its ancestor directories.
func (t *Tree) AddDirectory(relativePath string) error {
	trimmed := strings.TrimSuffix(relativePath, "/")
	if trimmed == "" || trimmed == "." {
		return nil
	}
	cleaned, err := cleanOccupiedPath(trimmed)
	if err != nil {
		return &Error{Path: relativePath, Reason: err.Error()}
	}
	if err := t.recordAncestors(cleaned); err != nil {
		return err
	}
	key := foldKey(cleaned)
	if existing, exists := t.occupied[key]; exists && !existing.isDirectory {
		return &Error{Path: cleaned, Reason: fmt.Sprintf("directory path conflicts with file %q", existing.spelling)}
	}
	t.occupied[key] = occupiedEntry{spelling: cleaned, isDirectory: true}
	return nil
}

// AddSymlink validates a relative symlink individually and stages it for
// whole-tree validation.
func (t *Tree) AddSymlink(relativePath, target string) error {
	if err := ValidateRelativeTarget(relativePath, target); err != nil {
		return err
	}
	cleaned, err := cleanRelativeEntryPath(relativePath)
	if err != nil {
		return &Error{Path: relativePath, Target: target, Reason: err.Error()}
	}
	key := foldKey(cleaned)
	if existing, exists := t.linksByKey[key]; exists {
		return &Error{
			Path:   cleaned,
			Target: target,
			Reason: fmt.Sprintf("symlink path collides with symlink %q", existing.Path),
		}
	}
	if err := t.recordAncestors(cleaned); err != nil {
		return &Error{Path: cleaned, Target: target, Reason: err.Error()}
	}
	entry := Entry{Path: cleaned, Target: target}
	t.linksByKey[key] = entry
	t.links = append(t.links, entry)
	return nil
}

// HasSymlink reports whether relativePath is staged as a symlink, matched
// case-insensitively.
func (t *Tree) HasSymlink(relativePath string) bool {
	cleaned, err := cleanRelativeEntryPath(relativePath)
	if err != nil {
		return false
	}
	_, exists := t.linksByKey[foldKey(cleaned)]
	return exists
}

// SharedSymlinkMatches reports whether relativePath was already staged as a
// symlink and whether its target equals target.
func (t *Tree) SharedSymlinkMatches(relativePath, target string) (bool, bool) {
	cleaned, err := cleanRelativeEntryPath(relativePath)
	if err != nil {
		return false, false
	}
	existing, exists := t.linksByKey[foldKey(cleaned)]
	if !exists {
		return false, false
	}
	return true, existing.Path == cleaned && existing.Target == target
}

// Validate checks the complete set of staged symlinks against every file,
// directory, ancestor, and sibling symlink in the tree, returning the symlinks
// in safe materialization order (referenced symlinks before links that traverse
// them, ties broken by ascending Path).
func (t *Tree) Validate() ([]Entry, error) {
	if len(t.links) == 0 {
		return nil, nil
	}

	sorted := slices.Clone(t.links)
	slices.SortFunc(sorted, func(left, right Entry) int {
		return strings.Compare(left.Path, right.Path)
	})

	for _, link := range sorted {
		for _, ancestor := range ancestorPaths(link.Path) {
			if ancestorLink, exists := t.linksByKey[foldKey(ancestor)]; exists {
				return nil, &Error{
					Path:   link.Path,
					Target: link.Target,
					Reason: fmt.Sprintf("path passes through symlink ancestor %q", ancestorLink.Path),
				}
			}
		}
	}

	for _, link := range sorted {
		if existing, exists := t.occupied[foldKey(link.Path)]; exists {
			if existing.isDirectory {
				return nil, &Error{
					Path:   link.Path,
					Target: link.Target,
					Reason: fmt.Sprintf("path is also used as directory %q", existing.spelling),
				}
			}
			return nil, &Error{
				Path:   link.Path,
				Target: link.Target,
				Reason: fmt.Sprintf("path collides with regular file %q", existing.spelling),
			}
		}
	}

	dependencies := make(map[string][]string, len(sorted))
	for _, link := range sorted {
		referenced, err := t.validateChain(link)
		if err != nil {
			return nil, err
		}
		dependencies[foldKey(link.Path)] = referenced
	}

	return topologicalOrder(sorted, t.linksByKey, dependencies), nil
}

// Materialize validates the tree and creates all staged symlinks inside root
// using an *os.Root handle so every path operation is kernel-bounded to root.
func (t *Tree) Materialize(root string) error {
	ordered, err := t.Validate()
	if err != nil || len(ordered) == 0 {
		return err
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = rootHandle.Close() }()

	for _, link := range ordered {
		parent := path.Dir(link.Path)
		if parent != "." {
			if err := rootHandle.MkdirAll(filepath.FromSlash(parent), 0o755); err != nil {
				return err
			}
		}
		if err := rootHandle.Symlink(filepath.FromSlash(link.Target), filepath.FromSlash(link.Path)); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tree) validateChain(link Entry) ([]string, error) {
	startKey := foldKey(link.Path)
	currentDirectory := parentComponents(link.Path)
	frames := []resolutionFrame{{
		linkKey:   startKey,
		remaining: strings.Split(link.Target, "/"),
	}}
	active := map[string]bool{startKey: true}
	var referenced []string
	hops := 0

	for len(frames) > 0 {
		topIndex := len(frames) - 1
		if len(frames[topIndex].remaining) == 0 {
			delete(active, frames[topIndex].linkKey)
			frames = frames[:topIndex]
			continue
		}
		component := frames[topIndex].remaining[0]
		frames[topIndex].remaining = frames[topIndex].remaining[1:]

		switch component {
		case "", ".":
			continue
		case "..":
			if len(currentDirectory) == 0 {
				return nil, &Error{
					Path:   link.Path,
					Target: link.Target,
					Reason: "target escapes the install root",
				}
			}
			currentDirectory = currentDirectory[:len(currentDirectory)-1]
		default:
			candidate := joinComponents(currentDirectory, component)
			candidateKey := foldKey(candidate)
			if targetLink, isLink := t.linksByKey[candidateKey]; isLink {
				if active[candidateKey] {
					return nil, &Error{
						Path:   link.Path,
						Target: link.Target,
						Reason: fmt.Sprintf("target forms a symlink cycle through %q", targetLink.Path),
					}
				}
				hops++
				if hops > maxSymlinkHops {
					return nil, &Error{
						Path:   link.Path,
						Target: link.Target,
						Reason: "target exceeds maximum symlink depth",
					}
				}
				if !slices.Contains(referenced, candidateKey) {
					referenced = append(referenced, candidateKey)
				}
				active[candidateKey] = true
				frames = append(frames, resolutionFrame{
					linkKey:   candidateKey,
					remaining: strings.Split(targetLink.Target, "/"),
				})
				continue
			}
			currentDirectory = append(currentDirectory, component)
		}
	}
	return referenced, nil
}

func topologicalOrder(sorted []Entry, byKey map[string]Entry, dependencies map[string][]string) []Entry {
	visited := make(map[string]bool, len(sorted))
	ordered := make([]Entry, 0, len(sorted))

	var visit func(key string)
	visit = func(key string) {
		if visited[key] {
			return
		}
		visited[key] = true
		dependencyKeys := slices.Clone(dependencies[key])
		slices.SortFunc(dependencyKeys, func(left, right string) int {
			return strings.Compare(byKey[left].Path, byKey[right].Path)
		})
		for _, dependencyKey := range dependencyKeys {
			visit(dependencyKey)
		}
		ordered = append(ordered, byKey[key])
	}

	for _, entry := range sorted {
		visit(foldKey(entry.Path))
	}
	return ordered
}

func (t *Tree) recordAncestors(cleanedPath string) error {
	for _, ancestor := range ancestorPaths(cleanedPath) {
		key := foldKey(ancestor)
		existing, exists := t.occupied[key]
		if exists && !existing.isDirectory {
			return &Error{
				Path:   cleanedPath,
				Reason: fmt.Sprintf("ancestor %q conflicts with regular file %q", ancestor, existing.spelling),
			}
		}
		if !exists {
			t.occupied[key] = occupiedEntry{spelling: ancestor, isDirectory: true}
		}
	}
	return nil
}

func cleanOccupiedPath(rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("entry path must not be empty")
	}
	if strings.ContainsRune(rawPath, 0) {
		return "", fmt.Errorf("entry path contains a NUL byte")
	}
	cleaned := path.Clean(strings.TrimPrefix(rawPath, "/"))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("entry path %q escapes the install root", rawPath)
	}
	return cleaned, nil
}

func cleanRelativeEntryPath(rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("entry path must not be empty")
	}
	if strings.ContainsRune(rawPath, 0) {
		return "", fmt.Errorf("entry path contains a NUL byte")
	}
	if strings.ContainsRune(rawPath, '\\') {
		return "", fmt.Errorf("entry path %q must not contain backslashes", rawPath)
	}
	if strings.HasPrefix(rawPath, "/") || strings.HasSuffix(rawPath, "/") || hasDrivePrefix(rawPath) {
		return "", fmt.Errorf("entry path %q must be a relative file or link path without leading or trailing '/'", rawPath)
	}
	cleaned := path.Clean(rawPath)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("entry path %q escapes the install root", rawPath)
	}
	return cleaned, nil
}

func ancestorPaths(cleanedPath string) []string {
	var ancestors []string
	for current := path.Dir(cleanedPath); current != "." && current != "/"; current = path.Dir(current) {
		ancestors = append(ancestors, current)
	}
	slices.Reverse(ancestors)
	return ancestors
}

func parentComponents(cleanedPath string) []string {
	parent := path.Dir(cleanedPath)
	if parent == "." {
		return nil
	}
	return strings.Split(parent, "/")
}

func joinComponents(directory []string, leaf string) string {
	if len(directory) == 0 {
		return leaf
	}
	return strings.Join(directory, "/") + "/" + leaf
}

func foldKey(cleanedPath string) string {
	return strings.ToLower(cleanedPath)
}

func hasDrivePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	first := value[0]
	return (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')
}
