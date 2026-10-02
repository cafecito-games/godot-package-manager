package slice

import "strings"

// IndexFileName is the name of the index document that describes an addon's
// published slices: the file `gpm package` writes beside the archives, and the
// asset name a consumer discovers a sliced addon by.
//
// It lives here because the producer and the consumer must agree on it forever,
// and this is the lowest layer both of them already import. Two spellings of one
// wire-format name is the defect this single declaration exists to prevent.
const IndexFileName = "gpm-index.toml"

// addonsDirectory is the project-relative directory every addon installs into.
// It is the single declaration of that layout: a producer packages the subtree
// that sits there, and a consumer extracts the slices back into it.
const addonsDirectory = "addons"

// AddonInstallPath returns the project-relative directory an addon installed
// under installName occupies, for instance "addons/limboai".
func AddonInstallPath(installName string) string {
	return addonsDirectory + "/" + installName
}

// AddonResourceRoot returns the res:// root of that directory, which is the root
// every .gdextension entry value of the addon resolves against.
func AddonResourceRoot(installName string) string {
	return resourcePrefix + AddonInstallPath(installName)
}

// RebaseEntries re-roots one slice's index entries for one .gdextension file,
// from the addon root they were published against to the root the addon is
// installed at, after checking that every path they name is inside the published
// root. It returns a new ExtensionEntries and never modifies its input.
//
// Containment is checked here because this is the first point at which an addon
// root is known. gpm-index.toml declares none, so LoadIndex can only establish
// that an entry value is a clean res:// path somewhere in the project: a
// published index could otherwise name a path outside the addon it belongs to
// and have it written into the installed .gdextension. The rule is the same one
// partitioning applies to an addon author's own file, made through the same
// comparison rather than a second implementation of it.
//
// Re-rooting is that rule read the other way round. An index's entry values are
// project-absolute paths under the addon's published directory, while a project
// may install the addon under a different directory name with install_as, so a
// value carried through unchanged would name a directory that does not exist.
// Both forms an author may write — a res:// path and a path relative to the
// .gdextension — are canonicalized to a project-absolute path while
// partitioning, so every published entry needs the same rewrite.
//
// A [dependencies] entry's Dictionary keys are dependency paths and obey both
// rules. Its values are export destinations relative to Godot's export
// directory rather than paths in the project, so they are neither checked nor
// rewritten.
//
// Every failure is an *output.FetchError: an index is remote content published
// by the addon's producer, and an entry naming a file outside the addon is never
// a reason to install a .gdextension that points out of it.
func RebaseEntries(
	id SliceID,
	extensionPath string,
	entries ExtensionEntries,
	publishedRoot, installedRoot string,
) (ExtensionEntries, error) {
	published, err := cleanAddonRoot(publishedRoot)
	if err != nil {
		return ExtensionEntries{}, fetchErrorf(
			"re-rooting the %s entries of slice %q: published %s", extensionSuffix, id, err)
	}
	installed, err := cleanAddonRoot(installedRoot)
	if err != nil {
		return ExtensionEntries{}, fetchErrorf(
			"re-rooting the %s entries of slice %q: installed %s", extensionSuffix, id, err)
	}
	roots := addonRoots{published: published, installed: installed}

	rebased := ExtensionEntries{}
	// One re-rooter per partitioned section, each binding that section's table
	// and how its leaf carries paths; the containment rule and the rewrite are
	// shared and live in rebasePath. They are keyed by section and run in
	// PartitionedSections order, so a section added to the vocabulary without a
	// re-rooter here is reported rather than installed unchecked.
	rebasers := map[ExtensionSection]func() error{
		SectionLibraries: func() error {
			table, err := rebaseTable(id, extensionPath, SectionLibraries, roots, entries.Libraries,
				rebaseContext.rebasePath)
			if err != nil {
				return err
			}
			rebased.Libraries = table
			return nil
		},
		SectionDependencies: func() error {
			table, err := rebaseTable(id, extensionPath, SectionDependencies, roots, entries.Dependencies,
				rebaseDependencyTargets)
			if err != nil {
				return err
			}
			rebased.Dependencies = table
			return nil
		},
	}
	for _, section := range PartitionedSections() {
		rebase, declared := rebasers[section]
		if !declared {
			return ExtensionEntries{}, fetchErrorf(
				"re-rooting the %s entries of slice %q: section [%s] is not re-rooted by this gpm",
				extensionSuffix, id, section,
			)
		}
		if err := rebase(); err != nil {
			return ExtensionEntries{}, err
		}
	}
	return rebased, nil
}

// addonRoots is the pair of res:// roots a re-rooting is between: the root the
// index published its values against and the root the project installs the addon
// at. They are one value because neither is meaningful without the other, and
// every section derives both from it, so the two sections cannot drift apart.
type addonRoots struct {
	published string
	installed string
}

// rebaseContext is everything one entry's diagnostic needs to name, carried
// together so each section's leaf rule reports the slice, the section, the file,
// and the entry key the same way.
type rebaseContext struct {
	id            SliceID
	section       ExtensionSection
	extensionPath string
	entryKey      string
	roots         addonRoots
}

// rebasePath applies the containment rule and the rewrite to one path. It is
// the [libraries] leaf rule as it stands, and the per-dependency rule a
// [dependencies] Dictionary's keys go through.
func (context rebaseContext) rebasePath(value string) (string, error) {
	// Re-checked rather than trusted from the index, because an Index may also
	// be assembled in memory by a producer rather than loaded from bytes.
	if err := validatePartitionedValue(value); err != nil {
		return "", context.wrap(err)
	}
	// The same comparison partitioning makes against an author's own file: the
	// value is already a clean res:// path, so containment is a lexical prefix
	// test that needs no disk access. The authored spelling and the resolved
	// value are one string here, because the index publishes only the resolved
	// form.
	if err := requireWithinAddonRoot(context.roots.published, value, value); err != nil {
		return "", context.wrap(err)
	}
	return context.roots.installed + strings.TrimPrefix(value, context.roots.published), nil
}

// wrap reports a leaf failure with the slice, section, file, and entry key it
// belongs to. The inner error is rendered by message rather than wrapped: it
// carries no exit code of its own, and the one this returns is the index's.
func (context rebaseContext) wrap(err error) error {
	return fetchErrorf(
		"index slice %q: %s entry %q in %q: %s",
		context.id, context.section, context.entryKey, context.extensionPath, err,
	)
}

// rebaseDependencyTargets re-roots one [dependencies] entry's Godot Dictionary.
// Only the dependency paths, which are its keys, are checked and rewritten; the
// export destinations they map to are relative to the export directory and are
// carried through untouched. Paths are visited in ascending order, so a
// Dictionary with several problems always reports the same one.
func rebaseDependencyTargets(
	context rebaseContext,
	targets ExtensionDependencyTargets,
) (ExtensionDependencyTargets, error) {
	rebased := make(ExtensionDependencyTargets, len(targets))
	for _, path := range sortedKeys(targets) {
		rebasedPath, err := context.rebasePath(path)
		if err != nil {
			return nil, err
		}
		rebased[rebasedPath] = targets[path]
	}
	return rebased, nil
}

// rebaseTable re-roots one slice's entries for one section. Entries are visited
// in ascending key order, so an entry table with several problems always reports
// the same one. A nil table stays nil, which is how an absent section stays
// absent rather than becoming a declared-but-empty one.
func rebaseTable[Value any](
	id SliceID,
	extensionPath string,
	section ExtensionSection,
	roots addonRoots,
	table ExtensionEntryTable[Value],
	rebaseValue func(rebaseContext, Value) (Value, error),
) (ExtensionEntryTable[Value], error) {
	if table == nil {
		return nil, nil
	}
	rebased := make(ExtensionEntryTable[Value], len(table))
	for _, entryKey := range sortedKeys(table) {
		context := rebaseContext{
			id: id, section: section, extensionPath: extensionPath, entryKey: entryKey, roots: roots,
		}
		value, err := rebaseValue(context, table[entryKey])
		if err != nil {
			return nil, err
		}
		rebased[entryKey] = value
	}
	return rebased, nil
}
