package slice

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser/configfile"
	"github.com/cafecito-games/gdparser/configfile/ast"
	"github.com/cafecito-games/gdparser/configfile/format"
)

// byteOrderMark is the UTF-8 byte order mark. Godot never writes one, but an
// author's editor may add it, and the config parser's lexer does not recognize
// one, so it is stripped before parsing rather than reported as a syntax error.
const byteOrderMark = "\ufeff"

// ExtensionEntries holds one slice's removed entries for one .gdextension file,
// with one field per partitioned section.
//
// The section dimension is load-bearing rather than cosmetic. A Godot platform
// tag such as "ios.template_release.arm64" legitimately appears in both
// [libraries] and [dependencies] — one names the platform's library, the other
// the files shipped beside it — so a shape with no section dimension would
// silently drop one of the two.
//
// It is a struct rather than a section-keyed map because the sections no longer
// share a value type, and a map would have to widen its leaf to the union of
// both and make [libraries] lossy. Each field is exactly the element type of the
// matching IndexSlice table, so a partition result is still written straight
// into an IndexSlice with no translation layer.
type ExtensionEntries struct {
	Libraries    ExtensionEntryTable[string]
	Dependencies ExtensionEntryTable[ExtensionDependencyTargets]
}

// PartitionExtension splits a .gdextension into the body the core slice ships
// and the per-slice entries the index publishes. The returned core keeps every
// section, key, value, and comment of the input except the platform-tagged
// entries, which move into the returned map; sections gpm does not recognize —
// [configuration], [icons], anything an author invented — keep their content and
// their order. The map groups the removed entries by the slice that owns the file
// each entry names, and then by the section it came from.
//
// Output is canonical: the emitted body is the config format's canonical
// spelling, so a UTF-8 BOM and CRLF line endings in the input are accepted and
// normalized away rather than reproduced. That is sound because this body is a
// file gpm generates into a slice archive rather than an edit of the author's own
// working file, and it makes determinism hold by construction.
//
// An entry is grouped under exactly the slice ID its key reduces to, which is
// what ReduceLibraryKey defines. A Godot tag carries an architecture only
// sometimes, so one platform's entries legitimately land under both its generic
// slice ID and an architecture-specific one: godot_jolt writes macos.editor
// beside macos.template_release.universal, and nobodywho writes macos.debug
// beside macos.debug.arm64. Deciding how such a platform is finally published —
// whether an architecture-less entry is fanned out into every architecture slice
// the addon ships, which the index schema accepts, since a key naming no
// architecture is valid inside an architecture-specific slice — is a whole-addon
// decision that needs the published slice set, so it belongs to the packager and
// not here. This function sees one file and reports what each key says.
//
// addonRoot is the addon's res:// root, for instance "res://addons/limboai", and
// extensionPath is this .gdextension file's own path relative to that root, for
// instance "godot_jolt.gdextension" or "sub/dir/thing.gdextension" — the same
// string the index uses as this file's section path key, so one spelling of a
// file's identity spans partition, index, and reassembly. Both are parameters
// rather than anything derived from disk because this function performs no
// filesystem access at all.
//
// Godot accepts an entry's path either as a res:// path or as a path relative to
// the .gdextension file's own location, and real addons ship both forms, so both
// are accepted here. A relative value is resolved against extensionPath's
// directory by string arithmetic alone, and the resolved value then obeys exactly
// the rules a res:// value does: it must be a clean res:// path, and it must name
// a file inside the addon subtree, checked lexically against addonRoot. A
// traversal component in a relative value is refused rather than simplified
// away, because a normalized traversal would land inside or outside the addon
// root depending only on how deep that root happens to be; a res:// path always
// names the same file unambiguously and is accepted instead.
//
// res:// is the one form published. The index stores the resolved value, so
// ReassembleExtension emits res:// for every entry whichever form the author
// wrote, which is what keeps the index's value grammar and determinism unchanged.
//
// Every failure is an *output.ManifestError: a .gdextension partitioned here is
// the addon author's own file, and a malformed one is their mistake to fix. The
// input buffer is never modified.
func PartitionExtension(content []byte, addonRoot, extensionPath string) ([]byte, map[SliceID]ExtensionEntries, error) {
	root, err := cleanAddonRoot(addonRoot)
	if err != nil {
		return nil, nil, manifestErrorf("partitioning .gdextension: %s", err)
	}
	directory, err := extensionDirectoryOf(extensionPath)
	if err != nil {
		return nil, nil, manifestErrorf("partitioning .gdextension: %s", err)
	}
	location := extensionLocation{addonRoot: root, directory: directory}
	file, err := parseExtensionDocument(content)
	if err != nil {
		return nil, nil, manifestErrorf("partitioning .gdextension: %s", err)
	}
	if err := validatePartitionedSectionNames(file); err != nil {
		return nil, nil, manifestErrorf("partitioning .gdextension: %s", err)
	}

	removed := map[SliceID]ExtensionEntries{}
	// One partitioner per partitioned section, each binding that section's value
	// reader and the ExtensionEntries field that holds it. The structural work is
	// shared and lives in partitionSection; only the leaf type differs, and a Go
	// method cannot be generic, so the binding is a closure per section. They are
	// keyed by section and run in PartitionedSections order, so a section added
	// to the vocabulary without a partitioner here is reported rather than
	// carried silently into the core body.
	partitioners := map[ExtensionSection]func() error{
		SectionLibraries: func() error {
			return partitionSection(file, location, removed, SectionLibraries, extensionLibraryValueOf,
				func(entries *ExtensionEntries) *ExtensionEntryTable[string] { return &entries.Libraries })
		},
		SectionDependencies: func() error {
			return partitionSection(file, location, removed, SectionDependencies, extensionDependencyTargetsOf,
				func(entries *ExtensionEntries) *ExtensionEntryTable[ExtensionDependencyTargets] {
					return &entries.Dependencies
				})
		},
	}
	for _, section := range PartitionedSections() {
		partition, declared := partitioners[section]
		if !declared {
			return nil, nil, manifestErrorf(
				"partitioning .gdextension: section [%s] is not partitioned by this gpm", section,
			)
		}
		if err := partition(); err != nil {
			return nil, nil, err
		}
	}
	return []byte(configfile.Format(file)), removed, nil
}

// partitionSection moves one section's platform-tagged entries out of the
// document and into removed, leaving the section's comments behind. readValue is
// the section's own value reader; tableOf names the ExtensionEntries field the
// values are stored in.
func partitionSection[Value any](
	file *ast.File,
	location extensionLocation,
	removed map[SliceID]ExtensionEntries,
	section ExtensionSection,
	readValue func(extensionLocation, *ast.Assignment) (Value, error),
	tableOf func(*ExtensionEntries) *ExtensionEntryTable[Value],
) error {
	block := sectionNamed(file, section)
	if block == nil {
		return nil
	}
	// Duplicates are tracked per section, because the same key in both sections
	// is the shape Godot expects rather than a mistake.
	seen := map[string]struct{}{}
	kept := make([]ast.Statement, 0, len(block.Statements))
	for _, statement := range block.Statements {
		assignment, isAssignment := statement.(*ast.Assignment)
		if !isAssignment {
			// A comment inside a partitioned section is content rather than an
			// entry, so it stays in the core body and no comment is lost.
			//
			// Its position relative to the entries cannot also be kept: the
			// entries are re-emitted sorted by key, which is what makes two
			// machines installing the same slice set write identical bytes, so
			// no entry is at its original index any more. The comments of a
			// partitioned section are therefore kept as one block in their own
			// original order, and reassembly writes the entries after them —
			// which is where the comments real .gdextension files carry in these
			// sections belong, since they are section and group labels such as
			// "; desktop" or a commented-out entry.
			kept = append(kept, statement)
			continue
		}
		owner, value, err := partitionedEntry(location, assignment, readValue)
		if err != nil {
			return manifestErrorf("partitioning .gdextension [%s]: %s", section, err)
		}
		if _, duplicate := seen[assignment.Key]; duplicate {
			return manifestErrorf(
				"partitioning .gdextension [%s]: key %q is declared more than once",
				section, assignment.Key,
			)
		}
		seen[assignment.Key] = struct{}{}

		entries := removed[owner]
		table := tableOf(&entries)
		if *table == nil {
			*table = ExtensionEntryTable[Value]{}
		}
		(*table)[assignment.Key] = value
		removed[owner] = entries
	}
	block.Statements = kept
	return nil
}

// ReassembleExtension rebuilds an installed .gdextension from the core body and
// the entries of the slices that were installed. entries is the index's
// per-slice entries for this one .gdextension file; selected is the installed
// slice set. A slice present in entries but not selected contributes nothing,
// which is how a project that installed only its own platforms ends up with a
// .gdextension describing exactly the binaries on disk.
//
// Output is deterministic and idempotent: entries are emitted sorted by key
// within their section, after whatever comments that section carries, the
// sections themselves keep the position the author gave them in the core body
// rather than being reordered, and the body is the config format's canonical
// spelling. Two machines installing the same slice set
// therefore write identical bytes, and a repeat install does not dirty the
// working tree. Validation walks PartitionedSections in order, so a file with
// problems in both sections always reports the same one.
//
// Every failure is an *output.FetchError: a core body and the entries beside it
// are remote content published by the addon's producer, and a mismatch between
// them is never a reason to install a .gdextension that misdescribes the tree.
// No filesystem access is performed.
func ReassembleExtension(core []byte, entries map[SliceID]ExtensionEntries, selected []SliceID) ([]byte, error) {
	file, err := parseExtensionDocument(core)
	if err != nil {
		return nil, fetchErrorf("reassembling .gdextension: %s", err)
	}
	if err := validatePartitionedSectionNames(file); err != nil {
		return nil, fetchErrorf("reassembling .gdextension: %s", err)
	}
	for _, section := range PartitionedSections() {
		block := sectionNamed(file, section)
		if block == nil {
			continue
		}
		if leftover := countAssignments(block); leftover > 0 {
			return nil, fetchErrorf(
				"reassembling .gdextension: the core body still declares %d entries in [%s], so it was published without being partitioned",
				leftover, section,
			)
		}
	}

	filled := map[ExtensionSection][]*ast.Assignment{}
	owners := map[ExtensionSection]map[string]SliceID{}
	for _, section := range PartitionedSections() {
		owners[section] = map[string]SliceID{}
	}

	for _, id := range deduplicatedSliceIDs(selected) {
		// The core slice carries no platform-tagged entry by definition, which is
		// also what the index schema enforces, so it is neither required to appear
		// in entries nor permitted to.
		if id.IsCore() {
			if _, declared := entries[id]; declared {
				return nil, fetchErrorf(
					"reassembling .gdextension: the %q slice declares entries, but platform-tagged entries belong to platform slices",
					CorePlatform,
				)
			}
			continue
		}
		sliceEntries, declared := entries[id]
		if !declared {
			return nil, fetchErrorf(
				"reassembling .gdextension: slice %q is installed but the index declares no entries for it",
				id,
			)
		}
		// One reassembler per partitioned section, bound and dispatched for the
		// same reasons as the partitioners: the structural work is shared, only
		// the leaf type and its rendering differ, and keying them by section
		// means a section added to the vocabulary without a reassembler here is
		// reported rather than dropped from the installed file.
		reassemblers := map[ExtensionSection]func() error{
			SectionLibraries: func() error {
				return reassembleSection(file, filled, owners, SectionLibraries, id,
					sliceEntries.Libraries, libraryExpressionOf)
			},
			SectionDependencies: func() error {
				return reassembleSection(file, filled, owners, SectionDependencies, id,
					sliceEntries.Dependencies, dependencyTargetsExpressionOf)
			},
		}
		for _, section := range PartitionedSections() {
			reassemble, declared := reassemblers[section]
			if !declared {
				return nil, fetchErrorf(
					"reassembling .gdextension: section [%s] is not reassembled by this gpm", section,
				)
			}
			if err := reassemble(); err != nil {
				return nil, err
			}
		}
	}

	for _, section := range PartitionedSections() {
		assignments := filled[section]
		sort.Slice(assignments, func(left, right int) bool { return assignments[left].Key < assignments[right].Key })
		block := sectionNamed(file, section)
		for _, assignment := range assignments {
			block.Statements = append(block.Statements, assignment)
		}
	}
	return []byte(configfile.Format(file)), nil
}

// reassembleSection validates one slice's entries for one section, in ascending
// key order, and places them in the section they belong to.
func reassembleSection[Value any](
	file *ast.File,
	filled map[ExtensionSection][]*ast.Assignment,
	owners map[ExtensionSection]map[string]SliceID,
	section ExtensionSection,
	id SliceID,
	table ExtensionEntryTable[Value],
	expressionOf func(Value) (ast.Expression, error),
) error {
	for _, key := range sortedKeys(table) {
		if err := reassembleEntry(file, filled, owners, section, id, key, table[key], expressionOf); err != nil {
			return err
		}
	}
	return nil
}

// reassembleEntry validates one index entry against the slice that declared it
// and places it in the section it belongs to. expressionOf both validates the
// section's leaf value and renders it as the Variant Godot reads back.
func reassembleEntry[Value any](
	file *ast.File,
	filled map[ExtensionSection][]*ast.Assignment,
	owners map[ExtensionSection]map[string]SliceID,
	section ExtensionSection,
	id SliceID,
	key string,
	value Value,
	expressionOf func(Value) (ast.Expression, error),
) error {
	owner, err := ReduceLibraryKey(key)
	if err != nil {
		// Reported by message rather than wrapped: ReduceLibraryKey returns a
		// *output.ManifestError, and output.CodeFor resolves that type before
		// *output.FetchError, so wrapping it would report an author's mistake for
		// what is remote content.
		return fetchErrorf("reassembling .gdextension [%s]: %s", section, err)
	}
	// A key naming no architecture belongs to every architecture of its platform,
	// which is how Godot writes iOS and macOS keys, so it is owned by an
	// architecture-specific slice too. This mirrors the index's own rule.
	if owner.Platform != id.Platform || (owner.Architecture != "" && owner.Architecture != id.Architecture) {
		return fetchErrorf(
			"reassembling .gdextension [%s]: key %q belongs to slice %q, not to slice %q",
			section, key, owner, id,
		)
	}
	expression, err := expressionOf(value)
	if err != nil {
		return fetchErrorf("reassembling .gdextension [%s] key %q: %s", section, key, err)
	}
	if previous, duplicate := owners[section][key]; duplicate {
		return fetchErrorf(
			"reassembling .gdextension [%s]: slices %q and %q both declare key %q",
			section, previous, id, key,
		)
	}
	if sectionNamed(file, section) == nil {
		return fetchErrorf(
			"reassembling .gdextension: slice %q declares a [%s] entry but the core body declares no [%s] section",
			id, section, section,
		)
	}
	owners[section][key] = id
	filled[section] = append(filled[section], &ast.Assignment{Key: key, Value: expression})
	return nil
}

// libraryExpressionOf validates a [libraries] value and renders it as the plain
// quoted string Godot expects.
func libraryExpressionOf(value string) (ast.Expression, error) {
	if err := validatePartitionedValue(value); err != nil {
		return nil, err
	}
	return &ast.StringLiteral{Value: value}, nil
}

// dependencyTargetsExpressionOf validates a [dependencies] value and renders it
// as Godot's Dictionary of dependency paths to export destinations.
//
// Emission is always the Dictionary spelling, even for an entry the author wrote
// as a bare string: the two spellings are Godot-equivalent and converge at the
// parser boundary, so the index holds exactly one representation and there is no
// "which spelling was it" flag to carry. That is in contract with emission
// already being canonical.
//
// The entries are appended in ascending dependency-path order, so two machines
// installing the same slice set emit identical bytes.
func dependencyTargetsExpressionOf(targets ExtensionDependencyTargets) (ast.Expression, error) {
	// Re-checked here rather than trusted from the index, because an Index may
	// also be assembled in memory by a producer.
	if err := validateDependencyTargets(targets); err != nil {
		return nil, err
	}
	paths := sortedKeys(targets)
	dictionary := &ast.DictionaryLiteral{Items: make([]ast.DictionaryItem, 0, len(paths))}
	for _, path := range paths {
		if err := validatePartitionedValue(path); err != nil {
			return nil, fmt.Errorf("dependency path %s", err)
		}
		dictionary.Items = append(dictionary.Items, &ast.DictionaryEntry{
			Key:   &ast.StringLiteral{Value: path},
			Value: &ast.StringLiteral{Value: targets[path]},
		})
	}
	return dictionary, nil
}

// parseExtensionDocument parses .gdextension bytes with the Godot config parser.
// It is the single entry point to that parser in this package, and no other
// package in the repository parses .gdextension content.
//
// A leading UTF-8 BOM is stripped first: the lexer does not recognize one and
// would report it as a stray key. Failures are returned untyped on purpose,
// because the same malformed file is an author's mistake while packaging and a
// producer's mistake while installing, so the exit code belongs to the caller.
func parseExtensionDocument(content []byte) (*ast.File, error) {
	source, _ := bytes.CutPrefix(content, []byte(byteOrderMark))
	file, err := configfile.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("%s is not a valid Godot configuration file: %s", extensionSuffix, err)
	}
	return file, nil
}

// sectionNamed returns the document's section for a partitioned section, or nil
// when the document declares no such section.
func sectionNamed(file *ast.File, section ExtensionSection) *ast.Section {
	for _, candidate := range file.Sections {
		if candidate != nil && candidate.Name == string(section) {
			return candidate
		}
	}
	return nil
}

// countAssignments counts a section's entries, ignoring its comments.
func countAssignments(section *ast.Section) int {
	count := 0
	for _, statement := range section.Statements {
		if _, isAssignment := statement.(*ast.Assignment); isAssignment {
			count++
		}
	}
	return count
}

// validatePartitionedSectionNames rejects the two section shapes that would make
// a partition lose entries rather than move them.
//
// A partitioned section declared twice is ambiguous: Godot merges repeated
// sections, but emptying one and leaving the other would silently drop entries,
// so the ambiguity is reported instead of resolved.
//
// A section differing from a partitioned one only in case is rejected rather than
// treated as unrecognized. Godot's section names are case-sensitive, so such a
// section is already broken; treating it as unknown would carry its
// platform-tagged entries straight into the core body, where they would name
// binaries no slice installs.
func validatePartitionedSectionNames(file *ast.File) error {
	seen := map[string]struct{}{}
	for _, section := range file.Sections {
		if section == nil {
			continue
		}
		if !slices.Contains(partitionedSections, ExtensionSection(section.Name)) {
			for _, partitioned := range partitionedSections {
				if strings.EqualFold(section.Name, string(partitioned)) {
					return fmt.Errorf(
						"section [%s] differs from [%s] only in case, and section names are case-sensitive",
						section.Name, partitioned,
					)
				}
			}
			continue
		}
		if _, found := seen[section.Name]; found {
			return fmt.Errorf("section [%s] is declared more than once", section.Name)
		}
		seen[section.Name] = struct{}{}
	}
	return nil
}

// partitionedEntry reads one platform-tagged entry: the slice that owns the file
// it names, and the section's own interpretation of its value. readValue both
// reads the Variant and validates it, because which Variant kinds are legal and
// which rules the leaf obeys are the section's own properties, while the key's
// reduction to a slice is shared.
func partitionedEntry[Value any](
	location extensionLocation,
	assignment *ast.Assignment,
	readValue func(extensionLocation, *ast.Assignment) (Value, error),
) (SliceID, Value, error) {
	var zero Value
	owner, err := ReduceLibraryKey(assignment.Key)
	if err != nil {
		// Reported by message for the same reason as in reassembleEntry: the inner
		// error's own type would decide the exit code of whatever wraps this one.
		return SliceID{}, zero, fmt.Errorf("%s", err)
	}
	value, err := readValue(location, assignment)
	if err != nil {
		return SliceID{}, zero, fmt.Errorf("key %q: %s", assignment.Key, err)
	}
	return owner, value, nil
}

// extensionLibraryValueOf reads a [libraries] entry's value, which is one plain
// quoted res:// path inside the addon.
//
// Godot accepts a Dictionary as a [dependencies] value but not as a [libraries]
// one, and the reader is selected by the section rather than by the Variant's
// kind, so a Dictionary here is still reported rather than interpreted.
func extensionLibraryValueOf(location extensionLocation, assignment *ast.Assignment) (string, error) {
	literal, isString := assignment.Value.(*ast.StringLiteral)
	if !isString {
		return "", fmt.Errorf(
			"value is a %s, and a partitioned entry names exactly one quoted %s path: %s",
			describeExpressionKind(assignment.Value), resourcePrefix, format.Expression(assignment.Value),
		)
	}
	path, err := plainStringOf(literal)
	if err != nil {
		return "", err
	}
	return location.resolveEntryPath(path)
}

// extensionDependencyTargetsOf reads a [dependencies] entry's value as Godot's
// Dictionary of dependency res:// paths to export destinations.
//
// Godot accepts two spellings for one such entry and real addons use both: a
// Dictionary, which is what limboai, loreline, sentry-godot and godot-sqlite
// ship, and a bare res:// path, which is what terrabrush ships. Rejecting either
// would make gpm refuse an addon Godot loads. They converge here, at the parser
// boundary, with a bare string normalized into the single-entry Dictionary with
// an empty destination that is its Godot equivalent, so the Go type and the
// index have exactly one representation of a dependency entry.
func extensionDependencyTargetsOf(location extensionLocation, assignment *ast.Assignment) (ExtensionDependencyTargets, error) {
	switch value := assignment.Value.(type) {
	case *ast.StringLiteral:
		path, err := plainStringOf(value)
		if err != nil {
			return nil, err
		}
		resolved, err := location.resolveEntryPath(path)
		if err != nil {
			return nil, err
		}
		return ExtensionDependencyTargets{resolved: ""}, nil
	case *ast.DictionaryLiteral:
		return dependencyTargetsOfDictionary(location, value)
	case *ast.TypedDictionaryLiteral:
		// Godot's Dictionary[KeyType, ValueType]({...}) spelling. Only a
		// String-to-String dictionary carries a dependency entry; any other
		// declared type is a Variant the index cannot represent, so it is
		// reported by its declared kind rather than read for its items.
		if !isStringTypeReference(value.KeyType) || !isStringTypeReference(value.ValueType) {
			return nil, fmt.Errorf(
				"value is a dictionary of %s to %s, and a [%s] entry maps quoted %s paths to export destinations: %s",
				describeTypeReference(value.KeyType), describeTypeReference(value.ValueType),
				SectionDependencies, resourcePrefix, format.Expression(assignment.Value),
			)
		}
		return dependencyTargetsOfDictionary(location, value.Value)
	default:
		return nil, fmt.Errorf(
			"value is a %s, and a [%s] entry is one quoted %s path or a dictionary mapping quoted %s paths to export destinations: %s",
			describeExpressionKind(assignment.Value), SectionDependencies,
			resourcePrefix, resourcePrefix, format.Expression(assignment.Value),
		)
	}
}

// dependencyTargetsOfDictionary reads a Dictionary's items as dependency paths
// and their export destinations.
//
// A comment among the items is accepted and dropped: the index carries no
// comment at this depth, and a partitioned section's own comments keep their
// separate treatment in the core body. A duplicate dependency path is reported
// rather than resolved last-wins, because a map assignment would silently drop a
// destination.
func dependencyTargetsOfDictionary(
	location extensionLocation,
	dictionary *ast.DictionaryLiteral,
) (ExtensionDependencyTargets, error) {
	targets := ExtensionDependencyTargets{}
	// The spelling each resolved path was written as, so a collision created by
	// resolution is reported by naming both spellings rather than only the
	// resolved path the author never wrote.
	spelling := map[string]string{}
	if dictionary != nil {
		for _, item := range dictionary.Items {
			entry, isEntry := item.(*ast.DictionaryEntry)
			if !isEntry {
				continue
			}
			path, err := dependencyDictionaryString(entry.Key, "dependency path")
			if err != nil {
				return nil, err
			}
			resolved, err := location.resolveEntryPath(path)
			if err != nil {
				return nil, err
			}
			destination, err := dependencyDictionaryString(entry.Value, fmt.Sprintf("destination of dependency %q", path))
			if err != nil {
				return nil, err
			}
			if err := validateExportDestination(destination); err != nil {
				return nil, err
			}
			// Checked after resolution, because one Dictionary is one map from a
			// dependency to its destination: two spellings of one dependency
			// carry two possibly different destinations for one file, which is
			// ambiguous rather than redundant.
			if previous, duplicate := spelling[resolved]; duplicate {
				if previous == path {
					return nil, fmt.Errorf("dependency %q is declared more than once", path)
				}
				return nil, fmt.Errorf(
					"dependencies %q and %q both name %q; one dictionary maps each dependency to one destination, so two spellings of one dependency are ambiguous",
					previous, path, resolved,
				)
			}
			spelling[resolved] = path
			targets[resolved] = destination
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf(
			"the dictionary names no dependency; an entry that ships nothing is omitted rather than declared empty",
		)
	}
	return targets, nil
}

// dependencyDictionaryString reads one Dictionary key or value as a plain
// string, naming the position for the diagnostic so an author is told which half
// of the entry is wrong.
func dependencyDictionaryString(expression ast.Expression, position string) (string, error) {
	literal, isString := expression.(*ast.StringLiteral)
	if !isString {
		return "", fmt.Errorf(
			"%s is a %s, and both halves of a dependency entry are quoted strings: %s",
			position, describeExpressionKind(expression), format.Expression(expression),
		)
	}
	value, err := plainStringOf(literal)
	if err != nil {
		return "", fmt.Errorf("%s: %s", position, err)
	}
	return value, nil
}

// plainStringOf decodes a string literal that carries no Variant prefix.
//
// A StringName or NodePath literal decodes to the same text as a plain string
// but is a different Variant type, so accepting one would change the value Godot
// reads back after reassembly.
func plainStringOf(literal *ast.StringLiteral) (string, error) {
	if literal.Prefix != "" {
		return "", fmt.Errorf(
			"value %s is a %s rather than a plain string",
			format.Expression(literal), describeStringPrefix(literal.Prefix),
		)
	}
	return literal.Value, nil
}

// extensionLocation is the frame of reference one .gdextension's entry values are
// read against: the addon's res:// root, which containment is checked against,
// and the .gdextension file's own directory relative to that root, which a
// relative value is resolved against. It is one value rather than two parameters
// because every reader needs both and neither is meaningful without the other.
type extensionLocation struct {
	addonRoot string

	// directory is the .gdextension file's directory relative to addonRoot, with
	// no trailing separator, and empty when the file sits at the addon root.
	directory string
}

// extensionDirectoryOf validates a .gdextension's own path relative to the addon
// root and returns the directory part of it, which is the frame a relative entry
// value is resolved against.
//
// The directory is taken with strings.LastIndex rather than with path/filepath,
// because partitioning performs no filesystem access and a path/filepath call
// would also apply the host's separator rules to a Godot resource path.
func extensionDirectoryOf(extensionPath string) (string, error) {
	// Never defaulted to the addon root: defaulting it would resolve a relative
	// value against a directory the file does not sit in and publish a path
	// naming a file that is not there.
	if extensionPath == "" {
		return "", fmt.Errorf(
			"the %s file's own path relative to the addon root is required", extensionSuffix,
		)
	}
	if err := validateExtensionPath(extensionPath); err != nil {
		return "", fmt.Errorf("invalid %s path: %s", extensionSuffix, err)
	}
	separator := strings.LastIndex(extensionPath, "/")
	if separator < 0 {
		return "", nil
	}
	return extensionPath[:separator], nil
}

// resolveEntryPath applies the rules every path a partition reads obeys, in the
// one order they are stated: a relative value is resolved against the
// .gdextension's own directory, and the resolved value must then be a clean
// res:// path naming a file inside the addon subtree. A dependency path is
// resolved and checked exactly like a library path, because both locate a file
// the slice archive carries, and a [dependencies] Dictionary's keys are
// dependency paths at that same depth.
//
// A res:// value resolves to itself, which is what makes the published form a
// fixpoint: partitioning an emitted file yields the entries it was emitted from.
func (location extensionLocation) resolveEntryPath(value string) (string, error) {
	resolved := value
	if !strings.HasPrefix(value, resourcePrefix) {
		if err := requireNoTraversalComponent(value); err != nil {
			return "", err
		}
		if err := validateRelativePath(value); err != nil {
			return "", fmt.Errorf(
				"value %q is neither a %s path nor a path relative to the %s file: %s",
				value, resourcePrefix, extensionSuffix, err,
			)
		}
		if location.directory == "" {
			resolved = location.addonRoot + "/" + value
		} else {
			resolved = location.addonRoot + "/" + location.directory + "/" + value
		}
	}
	if err := validatePartitionedValue(resolved); err != nil {
		return "", err
	}
	if err := requireWithinAddonRoot(location.addonRoot, resolved, value); err != nil {
		return "", err
	}
	return resolved, nil
}

// requireNoTraversalComponent refuses a traversal in a relative entry value
// before it is resolved, rather than normalizing it away.
//
// validateRelativePath already refuses one for every other path this package
// accepts, and normalize-then-check would be the weaker rule: "bin/../../x/lib.so"
// would land inside the addon root or outside it depending only on how deep that
// root happens to be, so one authored value would be accepted for one addon and
// refused for another. Nothing becomes unpackageable, because a res:// path can
// always name the same file, which is what the diagnostic says.
func requireNoTraversalComponent(value string) error {
	for _, component := range strings.Split(value, "/") {
		if component == ".." {
			return fmt.Errorf(
				"value %q has a %q component; a traversal is rejected rather than simplified, and a %s path names the same file instead",
				value, "..", resourcePrefix,
			)
		}
	}
	return nil
}

// isStringTypeReference reports whether a declared container type is plain
// String, which is the only key or value type a dependency entry may declare.
func isStringTypeReference(reference *ast.TypeRef) bool {
	return reference != nil && reference.Name == "String" && len(reference.Arguments) == 0
}

// describeTypeReference names a declared container type for a diagnostic. Only
// the type's own name is reported, because that is the component the rule is
// about and the whole offending value is already printed beside it.
func describeTypeReference(reference *ast.TypeRef) string {
	if reference == nil || reference.Name == "" {
		return "an unnamed type"
	}
	return reference.Name
}

// describeExpressionKind names a Variant kind for a diagnostic, so an author is
// told what they wrote rather than only what was expected.
func describeExpressionKind(expression ast.Expression) string {
	switch expression.(type) {
	case *ast.DictionaryLiteral, *ast.TypedDictionaryLiteral:
		return "dictionary"
	case *ast.ArrayLiteral, *ast.TypedArrayLiteral:
		return "array"
	case *ast.IntegerLiteral, *ast.FloatLiteral, *ast.UnaryExpression:
		return "number"
	case *ast.BoolLiteral:
		return "boolean"
	case *ast.NullLiteral:
		return "null"
	case *ast.Identifier:
		return "bare identifier"
	case *ast.ConstructorCall:
		return "constructor call"
	default:
		return "value of another type"
	}
}

// describeStringPrefix names the Variant type a string literal's prefix selects.
func describeStringPrefix(prefix string) string {
	switch prefix {
	case "&":
		return "StringName"
	case "^":
		return "NodePath"
	default:
		return "prefixed string"
	}
}

// cleanAddonRoot validates the addon root and strips a trailing separator, so a
// root written either way compares the same way.
func cleanAddonRoot(addonRoot string) (string, error) {
	root := strings.TrimSuffix(addonRoot, "/")
	if err := validateResourcePath(root); err != nil {
		return "", fmt.Errorf("invalid addon root: %s", err)
	}
	return root, nil
}

// requireWithinAddonRoot rejects an entry naming a file outside the addon. A
// slice may only carry files from within the addon subtree, because a slice
// archive is extracted into the addon's own directory. Both paths are already
// known to be clean res:// paths, so comparing them lexically is exact and needs
// no disk access.
//
// value is the resolved path the rule is about and authored is the spelling the
// addon's author wrote. They differ only for a value written relative to the
// .gdextension, and then the diagnostic names both, so an author is told what
// they wrote, what it resolved to, and the root it left.
func requireWithinAddonRoot(root, value, authored string) error {
	if strings.HasPrefix(value, root+"/") {
		return nil
	}
	if authored != value {
		return fmt.Errorf(
			"value %q resolves to %q, which is outside the addon root %q; a slice carries only files from within the addon subtree",
			authored, value, root,
		)
	}
	return fmt.Errorf(
		"value %q is outside the addon root %q; a slice carries only files from within the addon subtree",
		value, root,
	)
}

// validatePartitionedValue rejects an entry value that is not a clean res:// path
// inside the project. It is applied on the way in during partition and again on
// the way out during reassembly, so an index assembled by hand cannot produce a
// .gdextension naming a file outside the project.
//
// A quote or a control character is rejected even though the emitter would escape
// it: these values come from an addon's author and from a downloaded index, and a
// path spelled that way is a producer mistake or an injection attempt rather than
// a file anyone ships.
func validatePartitionedValue(value string) error {
	if err := validateResourcePath(value); err != nil {
		return err
	}
	if strings.Contains(value, `"`) {
		return fmt.Errorf("path %q must not contain a quote", value)
	}
	if strings.ContainsFunc(value, func(character rune) bool { return character < ' ' || character == 0x7f }) {
		return fmt.Errorf("path contains a control character")
	}
	return nil
}

// deduplicatedSliceIDs orders a selected set the way a selection is ordered, so
// that a diagnostic over several slices always names the same one first.
func deduplicatedSliceIDs(ids []SliceID) []SliceID {
	set := make(map[SliceID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return sortedSliceIDs(set)
}

// sortedKeys returns a table's keys in sorted order, so that validation visits
// them deterministically, reports the same failure every run, and emits the same
// bytes on two machines. It is generic in the value because it is applied at
// every level of a partitioned section, down to a dependency entry's own table.
func sortedKeys[Value any](table map[string]Value) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
