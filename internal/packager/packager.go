package packager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// IndexFileName is the index `gpm package` writes beside the slice archives.
const IndexFileName = "gpm-index.toml"

// DefaultOutputDirectory is where archives and the index go when --out is not
// given. It is relative to the addon repository's root.
const DefaultOutputDirectory = "dist"

// resourcePrefix is the Godot resource scheme the addon root is spelled with.
// PartitionExtension takes that root as a parameter and canonicalizes every
// entry value against it, so this is the one place the packager needs the
// scheme; the grammar of a resource path, and every rule it obeys, stay
// internal/slice's.
const resourcePrefix = "res://"

// Options are the inputs `gpm package` collects from its flags.
type Options struct {
	// Directory is the addon repository's root, which holds gpm-package.toml.
	// Empty means the working directory. `gpm package` runs in an addon
	// repository, which has no project.godot, so no project discovery applies.
	Directory string

	// OutputDirectory is where archives and the index are written. Empty means
	// DefaultOutputDirectory; a relative path is resolved against Directory, so
	// the default lands in the addon repository rather than wherever the author
	// happened to be standing.
	OutputDirectory string

	// Version overrides [package] version.
	Version string
}

// Result is what one packaging run produced.
type Result struct {
	Name    string `json:"name"`
	Version string `json:"version"`

	// Index is the path of the emitted gpm-index.toml.
	Index string `json:"index"`

	// Slices are the published slices, core first and the rest in ascending
	// slice-ID order.
	Slices []SliceResult `json:"slices"`
}

// SliceResult is one published slice of a packaging run.
type SliceResult struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Package turns one addon subtree into a core archive plus one archive per
// platform slice, described by a gpm-index.toml.
//
// Every published slice is independently complete: installing it alone beside
// core yields a .gdextension describing every binary that slice ships for its
// platform. No network access is performed, the addon subtree is only ever read,
// and nothing is written outside the output directory.
func Package(options Options) (*Result, error) {
	repositoryRoot, err := filepath.Abs(options.Directory)
	if err != nil {
		return nil, manifestErrorf("locating the addon repository at %q: %s", options.Directory, err)
	}
	configPath := filepath.Join(repositoryRoot, ConfigFileName)
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	version := options.Version
	if version == "" {
		version = config.Package.Version
	}
	if err := validateVersion(version); err != nil {
		return nil, manifestErrorf("%s: %s", configPath, err)
	}
	// The name passed manifest.ValidateAddonName while the config was loaded,
	// which is the rule for an addon's directory name. An archive's name is also
	// a published asset name the index schema has its own rules for, and they are
	// stricter in one respect, so the name is checked against them here rather
	// than discovered when the emitted index is rejected.
	if err := validateAssetNameComponent("[package] name", config.Package.Name); err != nil {
		return nil, manifestErrorf("%s: %s", configPath, err)
	}

	if err := requireInstallPath(config.Package.Name, config.Package.AddonPath); err != nil {
		return nil, manifestErrorf("%s: %s", configPath, err)
	}
	addonRoot, err := resolveAddonRoot(repositoryRoot, config.Package.AddonPath)
	if err != nil {
		return nil, err
	}
	tree, err := walkAddonTree(repositoryRoot, config.Package.AddonPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tree.close() }()

	// The addon's res:// root, which is both where the subtree sits in the
	// author's own project and where a consumer installs it. requireInstallPath
	// above makes those the same string, so an entry value the author wrote as a
	// res:// path in their working project is also the value a consumer resolves.
	resourceRoot := resourcePrefix + config.Package.AddonPath

	partitioned, coreBodies, err := partitionExtensions(tree, resourceRoot)
	if err != nil {
		return nil, err
	}
	declared, err := declaredSliceIDs(config.Package.Slices)
	if err != nil {
		return nil, err
	}
	plan := newFanOutPlan(sortedSliceIDs(partitioned), declared)
	published, err := plan.applyTo(partitioned)
	if err != nil {
		return nil, err
	}

	claims := newClaimSet()
	if err := claimEntryFiles(claims, tree, partitioned, plan, resourceRoot); err != nil {
		return nil, err
	}
	extras, err := claimExtras(claims, tree, config.Package.Slices, declared, plan)
	if err != nil {
		return nil, err
	}
	if err := rejectGenericSliceBesideItsArchitectures(published, extras); err != nil {
		return nil, err
	}

	coreID := slice.CoreSliceID()
	membership, err := claims.resolve(tree, coreID)
	if err != nil {
		return nil, err
	}

	outputDirectory, err := prepareOutputDirectory(repositoryRoot, options.OutputDirectory, addonRoot)
	if err != nil {
		return nil, err
	}

	// Removed before the first archive is written, so a run that fails partway
	// leaves no index at all rather than one describing archives whose bytes have
	// already been replaced. An index that no longer matches the archives beside
	// it is the one output of this command an author could upload without
	// noticing it is wrong.
	indexPath := filepath.Join(outputDirectory, IndexFileName)
	if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
		return nil, installErrorf("removing the previous %s: %s", IndexFileName, err)
	}

	index := &slice.Index{
		Format:  slice.SupportedIndexFormat,
		Name:    config.Package.Name,
		Version: version,
		Slices:  map[string]*slice.IndexSlice{},
	}
	result := &Result{Name: config.Package.Name, Version: version, Index: indexPath}

	for _, id := range publishedSliceIDs(coreID, membership, published, extras) {
		files, err := archiveFilesOf(id, coreID, tree, membership[id], coreBodies)
		if err != nil {
			return nil, err
		}
		fileName := archiveFileName(config.Package.Name, version, id)
		archivePath := filepath.Join(outputDirectory, fileName)
		if err := writeArchive(archivePath, files); err != nil {
			return nil, err
		}
		// Measured from the bytes that landed on disk, after the archive was
		// renamed into place, so what the index pins is what a consumer
		// downloads.
		checksum, size, err := measureArchive(archivePath)
		if err != nil {
			return nil, err
		}
		indexSlice := &slice.IndexSlice{File: fileName, SHA256: checksum, Size: size}
		if entries, declared := published[id]; declared {
			if err := fillIndexSlice(indexSlice, id, entries); err != nil {
				return nil, err
			}
		}
		index.Slices[id.String()] = indexSlice
		result.Slices = append(result.Slices, SliceResult{
			ID: id.String(), File: fileName, Size: size, SHA256: checksum,
		})
	}

	// The index is written last, after every archive it describes exists and has
	// been hashed, so an interrupted run never leaves an index pinning an archive
	// that is not there.
	if err := index.Save(result.Index); err != nil {
		// Reported by message inside an install error: Save wraps every
		// filesystem failure — the temp file, the write, the sync, the rename — in
		// an *output.ManifestError, and output.CodeFor resolves that type first, so
		// wrapping it with %w would report a disk failure in --out as a config
		// mistake. Writing the index into --out is a filesystem failure like any
		// other write this command performs.
		return nil, installErrorf("writing %s: %s", IndexFileName, err)
	}
	if err := verifyEmittedIndex(result.Index); err != nil {
		return nil, err
	}
	return result, nil
}

// partitionExtensions partitions every .gdextension in the addon subtree.
//
// It returns the per-slice entries keyed by slice and then by the .gdextension's
// path relative to the addon root, and the core body of each .gdextension. The
// path key is the tree walk's own relative path, which is also the extensionPath
// PartitionExtension resolves relative entry values against and the section path
// key the index publishes, so one spelling of a file's identity spans all three.
func partitionExtensions(
	tree *addonTree,
	resourceRoot string,
) (map[slice.SliceID]map[string]slice.ExtensionEntries, map[string][]byte, error) {
	partitioned := map[slice.SliceID]map[string]slice.ExtensionEntries{}
	coreBodies := map[string][]byte{}
	for _, file := range tree.files {
		if !strings.HasSuffix(file.relativePath, extensionSuffix) {
			continue
		}
		// Read through the same guard the archives use rather than with
		// os.ReadFile, which follows a symlink: the tree walk recorded a path, and
		// the file behind it is re-resolved here, so a .gdextension replaced by a
		// link would otherwise partition a file from outside the addon root into
		// the published core body.
		content, err := readRegularFile(tree.sourceAt(file.relativePath))
		if err != nil {
			return nil, nil, err
		}
		core, removed, err := slice.PartitionExtension(content, resourceRoot, file.relativePath)
		if err != nil {
			// Reported by message rather than wrapped: the inner error is already
			// an *output.ManifestError, so wrapping would nest one exit-code
			// carrier inside another.
			return nil, nil, manifestErrorf("%s: %s", file.relativePath, err)
		}
		coreBodies[file.relativePath] = core
		for id, entries := range removed {
			if partitioned[id] == nil {
				partitioned[id] = map[string]slice.ExtensionEntries{}
			}
			partitioned[id][file.relativePath] = entries
		}
	}
	return partitioned, coreBodies, nil
}

// claimEntryFiles assigns the files each partitioned .gdextension entry names to
// the slices that carry them.
//
// The slices are the fan-out plan's targets for the entry's partitioned slice,
// which is the one place the fan-out rule is applied to archive membership. The
// same plan decides the index entries, so the archives and the index cannot
// disagree about which slice carries what.
func claimEntryFiles(
	claims *claimSet,
	tree *addonTree,
	partitioned map[slice.SliceID]map[string]slice.ExtensionEntries,
	plan fanOutPlan,
	resourceRoot string,
) error {
	for _, source := range sortedSliceIDs(partitioned) {
		targets := plan.targetsOf(source)
		for _, extensionPath := range sortedKeys(partitioned[source]) {
			entries := partitioned[source][extensionPath]
			for _, key := range sortedKeys(entries.Libraries) {
				origin := entryOrigin(slice.SectionLibraries, key, extensionPath)
				if err := claimEntryValue(claims, tree, resourceRoot, entries.Libraries[key], origin, source, targets); err != nil {
					return err
				}
			}
			for _, key := range sortedKeys(entries.Dependencies) {
				targetTable := entries.Dependencies[key]
				origin := entryOrigin(slice.SectionDependencies, key, extensionPath)
				// An entry naming no dependency is never emitted: the index
				// schema rejects an empty dictionary, and reaching this with one
				// would mean the mapping above lost a dependency rather than that
				// the author declared nothing.
				if len(targetTable) == 0 {
					return manifestErrorf("%s names no dependency", origin)
				}
				// Every dictionary key is a file the slice must carry, so each one
				// obeys exactly the rule a [libraries] value obeys. The
				// destination beside it names an export-time subdirectory rather
				// than anything in the addon tree, so it is validated and copied
				// into the index but never stat-ed, archived, or created.
				for _, dependencyPath := range sortedKeys(targetTable) {
					if err := claimEntryValue(claims, tree, resourceRoot, dependencyPath, origin, source, targets); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// claimEntryValue claims the files one entry value names for every slice the
// fan-out plan routes the entry's partitioned slice to.
func claimEntryValue(
	claims *claimSet,
	tree *addonTree,
	resourceRoot, value, origin string,
	source slice.SliceID,
	targets []slice.SliceID,
) error {
	files, err := entryFilesOf(tree, resourceRoot, value, origin)
	if err != nil {
		return err
	}
	for _, target := range targets {
		for _, relativePath := range files {
			claims.add(relativePath, fileClaim{id: target, origin: origin, fannedOut: target != source})
		}
	}
	return nil
}

// entryFilesOf maps one entry value back to the files of the addon subtree it
// names.
//
// The value is already a project-absolute res:// path: PartitionExtension
// canonicalizes a value an author wrote relative to the .gdextension and checks
// it lexically against the addon root. The containment rule is therefore applied
// on the resolved value, here and there, and never on the spelling the author
// wrote. Resolution also goes through the tree walk rather than the filesystem,
// which is the second half of the guarantee: a path that is not a walked regular
// file is not archivable, whatever it happens to be on disk.
//
// A value naming a directory resolves to every file beneath it, because a macOS
// .framework and an iOS .xcframework are directories a [libraries] entry points
// straight at.
func entryFilesOf(tree *addonTree, resourceRoot, value, origin string) ([]string, error) {
	prefix := resourceRoot + "/"
	if !strings.HasPrefix(value, prefix) {
		return nil, manifestErrorf(
			"%s names %q, which is outside the addon root %q", origin, value, resourceRoot,
		)
	}
	relativePath := strings.TrimPrefix(value, prefix)
	files := tree.resolve(relativePath)
	if len(files) == 0 {
		return nil, manifestErrorf(
			"%s names %q, which is not a file in the addon subtree; a slice may not promise a binary that is not there",
			origin, value,
		)
	}
	return files, nil
}

// entryOrigin describes one .gdextension entry for a diagnostic.
func entryOrigin(section slice.ExtensionSection, key, extensionPath string) string {
	return fmt.Sprintf("%s [%s] key %q", extensionPath, section, key)
}

// claimExtras assigns the files each [package.slices] glob matches to the slice
// it names, and returns the slice IDs the extras declare.
//
// A glob matching nothing is refused rather than ignored: an extras list is how
// an author ships the files no .gdextension references, so a pattern that names
// none of them is a stale config, and silently publishing a release without an
// .aar or an .xcframework is exactly the failure extras exist to prevent.
func claimExtras(
	claims *claimSet,
	tree *addonTree,
	extras map[string][]string,
	declared []slice.SliceID,
	plan fanOutPlan,
) (map[slice.SliceID]struct{}, error) {
	claimed := map[slice.SliceID]struct{}{}
	tags := sortedKeys(extras)
	for position, tag := range tags {
		id := declared[position]
		if plan.suppresses(id) {
			return nil, manifestErrorf(
				"[package.slices] names slice %q, which is not published: %q declares both architecture-less and architecture-specific entries, so its entries ship in %s instead; name those slices",
				id, id.Platform, describeSliceIDs(plan.targetsOf(id)),
			)
		}
		claimed[id] = struct{}{}
		for _, pattern := range extras[tag] {
			matched, err := matchExtras(tree, id, pattern)
			if err != nil {
				return nil, err
			}
			origin := fmt.Sprintf("[package.slices] pattern %q", pattern)
			for _, relativePath := range matched {
				claims.add(relativePath, fileClaim{id: id, origin: origin})
			}
		}
	}
	return claimed, nil
}

// declaredSliceIDs parses the [package.slices] keys into slice IDs, in the same
// ascending key order claimExtras walks them in, so the two cannot disagree
// about which ID a key is.
//
// The keys were already validated while the config was loaded, so a parse
// failure here is unreachable; it is reported rather than ignored because the
// alternative is carrying an unparsed tag into the fan-out.
func declaredSliceIDs(extras map[string][]string) ([]slice.SliceID, error) {
	tags := sortedKeys(extras)
	ids := make([]slice.SliceID, 0, len(tags))
	for _, tag := range tags {
		id, err := slice.ParseDeclaredPlatform(tag)
		if err != nil {
			return nil, manifestErrorf("invalid [package.slices] key %q: %s", tag, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// matchExtras returns the files one extras pattern names.
//
// A pattern that exactly names a directory resolves to every file beneath it,
// for the same reason a [libraries] value pointing at an .xcframework does: a
// bundle is a directory, and an author writing its name means the bundle.
func matchExtras(tree *addonTree, id slice.SliceID, pattern string) ([]string, error) {
	var matched []string
	for _, file := range tree.files {
		if matchExtraPattern(pattern, file.relativePath) {
			matched = append(matched, file.relativePath)
		}
	}
	if len(matched) == 0 {
		matched = tree.resolve(pattern)
	}
	if len(matched) == 0 {
		return nil, manifestErrorf(
			"[package.slices] %q pattern %q matches no file in the addon subtree", id, pattern,
		)
	}
	return matched, nil
}

// rejectGenericSliceBesideItsArchitectures refuses a published set holding both
// a platform's generic slice and an architecture slice of that platform.
//
// The fan-out removes that shape wherever a .gdextension creates it, but an
// extras list can create it too, by naming a generic platform whose partitioned
// entries all carry an architecture. The consequence is the same either way: the
// host candidate chain installs the first published match, so a host resolving to
// the architecture slice would never see the generic slice's files, and gpm would
// report no error. It is refused rather than fanned out, because which
// architectures the author meant is exactly what their config did not say.
func rejectGenericSliceBesideItsArchitectures(
	published map[slice.SliceID]map[string]slice.ExtensionEntries,
	extras map[slice.SliceID]struct{},
) error {
	all := map[slice.SliceID]struct{}{}
	for id := range published {
		all[id] = struct{}{}
	}
	for id := range extras {
		all[id] = struct{}{}
	}
	for _, id := range sortedSliceIDs(all) {
		if id.Architecture != "" {
			continue
		}
		var architectures []slice.SliceID
		for _, candidate := range sortedSliceIDs(all) {
			if candidate.Platform == id.Platform && candidate.Architecture != "" {
				architectures = append(architectures, candidate)
			}
		}
		if len(architectures) == 0 {
			continue
		}
		return manifestErrorf(
			"slice %q would be published beside %s; a host installs the first slice of its platform it finds, so the files of %q would never be installed on a host that resolves to an architecture, and the extras naming %q must name those architecture slices instead",
			id, describeSliceIDs(architectures), id, id,
		)
	}
	return nil
}

// publishedSliceIDs returns the slices to publish, core first and the rest in
// ascending order. Core is always published, because the index requires it and
// every project needs it.
func publishedSliceIDs(
	coreID slice.SliceID,
	membership map[slice.SliceID][]string,
	published map[slice.SliceID]map[string]slice.ExtensionEntries,
	extras map[slice.SliceID]struct{},
) []slice.SliceID {
	all := map[slice.SliceID]struct{}{coreID: {}}
	for id := range membership {
		all[id] = struct{}{}
	}
	for id := range published {
		all[id] = struct{}{}
	}
	for id := range extras {
		all[id] = struct{}{}
	}
	delete(all, coreID)
	return append([]slice.SliceID{coreID}, sortedSliceIDs(all)...)
}

// archiveFilesOf builds one slice's archive contents.
//
// Core ships every .gdextension as its partitioned body rather than as the file
// on disk, which is what keeps the author's working tree untouched while the
// published core declares none of the entries the platform slices carry.
func archiveFilesOf(
	id, coreID slice.SliceID,
	tree *addonTree,
	members []string,
	coreBodies map[string][]byte,
) ([]archiveFile, error) {
	files := make([]archiveFile, 0, len(members))
	for _, relativePath := range members {
		if id == coreID && strings.HasSuffix(relativePath, extensionSuffix) {
			body, partitioned := coreBodies[relativePath]
			if !partitioned {
				return nil, manifestErrorf("%s was not partitioned", relativePath)
			}
			files = append(files, archiveFile{archivePath: relativePath, content: body})
			continue
		}
		files = append(files, tree.archiveFileAt(relativePath))
	}
	return files, nil
}

// fillIndexSlice copies one published slice's partitioned entries into its index
// table, walking the sections through their single declaration rather than
// naming them here.
func fillIndexSlice(
	indexSlice *slice.IndexSlice,
	id slice.SliceID,
	entries map[string]slice.ExtensionEntries,
) error {
	fillers := map[slice.ExtensionSection]func(){
		slice.SectionLibraries: func() {
			for _, extensionPath := range sortedKeys(entries) {
				table := entries[extensionPath].Libraries
				if len(table) == 0 {
					continue
				}
				if indexSlice.Libraries == nil {
					indexSlice.Libraries = slice.ExtensionSectionTable[string]{}
				}
				indexSlice.Libraries[extensionPath] = table
			}
		},
		slice.SectionDependencies: func() {
			for _, extensionPath := range sortedKeys(entries) {
				table := entries[extensionPath].Dependencies
				if len(table) == 0 {
					continue
				}
				if indexSlice.Dependencies == nil {
					indexSlice.Dependencies = slice.ExtensionSectionTable[slice.ExtensionDependencyTargets]{}
				}
				indexSlice.Dependencies[extensionPath] = table
			}
		},
	}
	for _, section := range slice.PartitionedSections() {
		fill, declared := fillers[section]
		if !declared {
			return manifestErrorf("slice %q: partitioned section %s is not written by this gpm", id, section)
		}
		fill()
	}
	return nil
}

// verifyEmittedIndex reads the index back and loads it through the schema's own
// loader.
//
// It is the last fail-closed gate of a run: every rule the index schema owns —
// the entry-key ownership a fanned-out slice depends on, the resource-path
// grammar, the export-destination grammar, the rejection of an empty dependency
// dictionary — is checked by internal/slice against the bytes that were actually
// written, rather than re-stated here. An index that does not load is removed, so
// a rejected run never leaves one an author could upload.
//
// LoadIndex returns an *output.FetchError, because an index is remote content to
// a consumer. Here it is the packager's own output, so the failure is reported by
// message inside a manifest error rather than wrapped: output.CodeFor resolves
// *output.ManifestError before *output.FetchError, and wrapping would report a
// fetch failure for an author's own tree.
func verifyEmittedIndex(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return installErrorf("reading back %s: %s", path, err)
	}
	if _, err := slice.LoadIndex(data); err != nil {
		_ = os.Remove(path)
		return manifestErrorf("the emitted %s is not a valid index: %s", IndexFileName, err)
	}
	return nil
}

// archiveFileName is the single spelling of a slice archive's name, used both
// for the file written to disk and for the index's file field, so the two cannot
// drift.
func archiveFileName(name, version string, id slice.SliceID) string {
	return fmt.Sprintf("%s-%s-%s.zip", name, version, id)
}

// resolveAddonRoot locates the addon subtree and refuses one that is not a real
// directory inside the repository.
//
// Every component is checked with os.Lstat rather than os.Stat, so a symlink
// anywhere along addon_path is refused before the tree is walked. A symlinked
// addon root would make the containment rule meaningless: the files archived
// would come from wherever the link points.
func resolveAddonRoot(repositoryRoot, addonPath string) (string, error) {
	current := repositoryRoot
	for _, component := range strings.Split(addonPath, "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return "", manifestErrorf("[package] addon_path %q names %s, which does not exist", addonPath, current)
			}
			return "", manifestErrorf("[package] addon_path %q: reading %s: %s", addonPath, current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", manifestErrorf(
				"[package] addon_path %q passes through the symlink %s; the addon subtree is read as it sits in the repository",
				addonPath, current,
			)
		}
		if !info.IsDir() {
			return "", manifestErrorf("[package] addon_path %q names %s, which is not a directory", addonPath, current)
		}
	}
	return current, nil
}

// prepareOutputDirectory resolves --out and creates it.
//
// A relative path is resolved against the repository root rather than the
// working directory, so the default lands in the addon repository being
// packaged. A directory inside the addon subtree is refused, and a directory
// that cannot be created is an *output.InstallError: the config and the tree
// were fine and the filesystem was not.
func prepareOutputDirectory(repositoryRoot, outputDirectory, addonRoot string) (string, error) {
	if outputDirectory == "" {
		outputDirectory = DefaultOutputDirectory
	}
	if !filepath.IsAbs(outputDirectory) {
		outputDirectory = filepath.Join(repositoryRoot, outputDirectory)
	}
	outputDirectory = filepath.Clean(outputDirectory)
	if err := requireOutsideAddonSubtree(outputDirectory, addonRoot); err != nil {
		return "", err
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return "", installErrorf("creating the output directory %s: %s", outputDirectory, err)
	}
	// Checked again once the directory exists, this time by file identity rather
	// than by comparing strings. The lexical check above catches the ordinary
	// mistake before anything is created; this one catches every spelling that
	// only reaches the addon subtree indirectly — through a symlinked ancestor,
	// through a hard link, or through a case alias on a case-insensitive
	// filesystem — none of which a string comparison can see.
	if err := requireIdentityOutsideAddonSubtree(outputDirectory, addonRoot); err != nil {
		return "", err
	}
	return outputDirectory, nil
}

// requireOutsideAddonSubtree refuses an output directory that is the addon
// subtree or sits inside it.
//
// Refused rather than allowed and warned about: archives written inside the
// addon subtree would be part of the subtree on the next run, so one release
// would carry the previous release's archives in its core slice, and the author
// would have no way to tell from the output that it happened.
func requireOutsideAddonSubtree(outputDirectory, addonRoot string) error {
	if outputDirectory != addonRoot && !strings.HasPrefix(outputDirectory, addonRoot+string(filepath.Separator)) {
		return nil
	}
	return manifestErrorf(
		"the output directory %s is inside the addon subtree %s; archives written there would be packaged into the next release",
		outputDirectory, addonRoot,
	)
}

// requireIdentityOutsideAddonSubtree refuses an output directory that is the
// addon subtree, or that has it as an ancestor, comparing the directories
// themselves rather than the paths that name them.
//
// It walks from the output directory up to the filesystem root, so a path whose
// spelling differs from the addon root's — a different case, a symlinked
// component, a hard-linked directory — is still recognized as being inside it.
func requireIdentityOutsideAddonSubtree(outputDirectory, addonRoot string) error {
	addonInfo, err := os.Stat(addonRoot)
	if err != nil {
		return installErrorf("reading the addon subtree %s: %s", addonRoot, err)
	}
	// Resolved before the ascent, not during it. Ascending the path as written
	// would compare the right directory at the bottom and the wrong ones above
	// it, so an output directory symlinked straight to a child of the addon root
	// would pass: its own identity is not the addon root's, and its lexical
	// parents are wherever the link happens to live.
	resolved, err := filepath.EvalSymlinks(outputDirectory)
	if err != nil {
		return installErrorf("resolving the output directory %s: %s", outputDirectory, err)
	}
	current := filepath.Clean(resolved)
	for {
		info, err := os.Stat(current)
		if err == nil && os.SameFile(info, addonInfo) {
			return manifestErrorf(
				"the output directory %s is inside the addon subtree %s; archives written there would be packaged into the next release",
				outputDirectory, addonRoot,
			)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// requireInstallPath refuses an addon_path that is not where the addon installs.
//
// A slice archive holds the addon subtree unprefixed and a consumer extracts it
// into addons/<name>, while every res:// value in the author's .gdextension is
// written against where the subtree sits in their own project. Those are the
// same directory only when addon_path is addons/<name>, and when they differ the
// emitted index names paths no consumer can resolve — with nothing in the output
// to say so. The spec describes only this layout, so it is required rather than
// guessed at.
func requireInstallPath(name, addonPath string) error {
	expected := "addons/" + name
	if addonPath == expected {
		return nil
	}
	return fmt.Errorf(
		"addon_path %q must be %q: a consumer installs the subtree at %s%s, and the %s values in the addon's own files are written against that path, so packaging from anywhere else would publish an index naming files no project has",
		addonPath, expected, resourcePrefix, expected, extensionSuffix,
	)
}

// validateVersion rejects a version that is missing or that could not appear in
// an archive's file name.
//
// The version is interpolated into <name>-<version>-<slice>.zip, which the index
// publishes as a bare asset name a consumer joins to a download location, so a
// separator, a colon, whitespace, or a control character in it would produce a
// name no consumer can resolve.
func validateVersion(version string) error {
	if version == "" {
		return fmt.Errorf("version is required; set [package] version or pass --version")
	}
	if version == "." || version == ".." {
		return fmt.Errorf("version %q is not a version", version)
	}
	return validateAssetNameComponent("version", version)
}

// validateAssetNameComponent rejects a value that could not appear in an
// archive's file name.
//
// Both the addon name and the version are interpolated into
// <name>-<version>-<slice>.zip, which the index publishes as a bare asset name a
// consumer joins to a download location and to a staging directory. A separator,
// a colon, whitespace, or a control character in either one would produce a name
// no consumer can resolve, so both are checked before any archive is written
// rather than when the emitted index is rejected.
func validateAssetNameComponent(label, value string) error {
	if strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%s %q must not contain a path separator", label, value)
	}
	if strings.Contains(value, ":") {
		return fmt.Errorf("%s %q must not contain a colon; an archive name is a bare asset name", label, value)
	}
	if strings.ContainsFunc(value, unicode.IsSpace) {
		return fmt.Errorf("%s %q must not contain whitespace", label, value)
	}
	if strings.ContainsFunc(value, func(character rune) bool { return character < ' ' || character == 0x7f }) {
		return fmt.Errorf("%s must not contain a control character", label)
	}
	return nil
}

// describeSliceIDs lists slice IDs for a diagnostic, in the order given.
func describeSliceIDs(ids []slice.SliceID) string {
	descriptions := make([]string, 0, len(ids))
	for _, id := range ids {
		descriptions = append(descriptions, fmt.Sprintf("%q", id.String()))
	}
	return strings.Join(descriptions, ", ")
}
