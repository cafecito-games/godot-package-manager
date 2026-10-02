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
// grouped by the section they came from.
//
// The section dimension is load-bearing rather than cosmetic. A Godot platform
// tag such as "ios.template_release.arm64" legitimately appears in both
// [libraries] and [dependencies] — one names the platform's library, the other
// the files shipped beside it — so a map with no section dimension would
// silently drop one of the two. Each section's table is the same
// map[string]string the index stores, so a partition result is written straight
// into an IndexSlice with no translation layer.
type ExtensionEntries map[ExtensionSection]map[string]string

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
// addonRoot is the addon's res:// root, for instance "res://addons/limboai". It
// is a parameter rather than something derived from a path on disk because this
// function performs no filesystem access at all: an entry's containment in the
// addon subtree is checked lexically against this prefix. Both the root and the
// entry values are rejected unless they are clean res:// paths, so a lexical
// prefix test is sound.
//
// Every failure is an *output.ManifestError: a .gdextension partitioned here is
// the addon author's own file, and a malformed one is their mistake to fix. The
// input buffer is never modified.
func PartitionExtension(content []byte, addonRoot string) ([]byte, map[SliceID]ExtensionEntries, error) {
	root, err := cleanAddonRoot(addonRoot)
	if err != nil {
		return nil, nil, manifestErrorf("partitioning .gdextension: %s", err)
	}
	file, err := parseExtensionDocument(content)
	if err != nil {
		return nil, nil, manifestErrorf("partitioning .gdextension: %s", err)
	}
	if err := validatePartitionedSectionNames(file); err != nil {
		return nil, nil, manifestErrorf("partitioning .gdextension: %s", err)
	}

	removed := map[SliceID]ExtensionEntries{}
	for _, section := range PartitionedSections() {
		block := sectionNamed(file, section)
		if block == nil {
			continue
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
			owner, value, err := partitionedEntry(root, assignment)
			if err != nil {
				return nil, nil, manifestErrorf("partitioning .gdextension [%s]: %s", section, err)
			}
			if _, duplicate := seen[assignment.Key]; duplicate {
				return nil, nil, manifestErrorf(
					"partitioning .gdextension [%s]: key %q is declared more than once",
					section, assignment.Key,
				)
			}
			seen[assignment.Key] = struct{}{}

			entries, found := removed[owner]
			if !found {
				entries = ExtensionEntries{}
				removed[owner] = entries
			}
			if entries[section] == nil {
				entries[section] = map[string]string{}
			}
			entries[section][assignment.Key] = value
		}
		block.Statements = kept
	}
	return []byte(configfile.Format(file)), removed, nil
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
		for _, section := range PartitionedSections() {
			for _, key := range sortedKeys(sliceEntries[section]) {
				if err := reassembleEntry(file, filled, owners, section, id, key, sliceEntries[section][key]); err != nil {
					return nil, err
				}
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

// reassembleEntry validates one index entry against the slice that declared it
// and places it in the section it belongs to.
func reassembleEntry(
	file *ast.File,
	filled map[ExtensionSection][]*ast.Assignment,
	owners map[ExtensionSection]map[string]SliceID,
	section ExtensionSection,
	id SliceID,
	key string,
	value string,
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
	if err := validatePartitionedValue(value); err != nil {
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
	filled[section] = append(filled[section], &ast.Assignment{Key: key, Value: &ast.StringLiteral{Value: value}})
	return nil
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
// it names, and the res:// path it points at.
func partitionedEntry(root string, assignment *ast.Assignment) (SliceID, string, error) {
	owner, err := ReduceLibraryKey(assignment.Key)
	if err != nil {
		// Reported by message for the same reason as in reassembleEntry: the inner
		// error's own type would decide the exit code of whatever wraps this one.
		return SliceID{}, "", fmt.Errorf("%s", err)
	}
	value, err := extensionValueOf(assignment)
	if err != nil {
		return SliceID{}, "", fmt.Errorf("key %q: %s", assignment.Key, err)
	}
	if err := validatePartitionedValue(value); err != nil {
		return SliceID{}, "", fmt.Errorf("key %q: %s", assignment.Key, err)
	}
	if err := requireWithinAddonRoot(root, value); err != nil {
		return SliceID{}, "", fmt.Errorf("key %q: %s", assignment.Key, err)
	}
	return owner, value, nil
}

// extensionValueOf reads a partitioned entry's value, which is one plain quoted
// res:// path.
//
// Godot also accepts a dictionary as a [dependencies] value, mapping each
// dependency to the subdirectory it is copied into on export. gpm's index
// publishes one path per key, so a dictionary is reported rather than flattened:
// flattening it would drop the destinations and install a .gdextension that no
// longer describes what Godot should copy.
func extensionValueOf(assignment *ast.Assignment) (string, error) {
	literal, isString := assignment.Value.(*ast.StringLiteral)
	if !isString {
		return "", fmt.Errorf(
			"value is a %s, and a partitioned entry names exactly one quoted %s path: %s",
			describeExpressionKind(assignment.Value), resourcePrefix, format.Expression(assignment.Value),
		)
	}
	// A StringName or NodePath literal decodes to the same text as a plain string
	// but is a different Variant type, so accepting one would change the value
	// Godot reads back after reassembly.
	if literal.Prefix != "" {
		return "", fmt.Errorf(
			"value %s is a %s rather than a plain string",
			format.Expression(literal), describeStringPrefix(literal.Prefix),
		)
	}
	return literal.Value, nil
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
func requireWithinAddonRoot(root, value string) error {
	if !strings.HasPrefix(value, root+"/") {
		return fmt.Errorf(
			"value %q is outside the addon root %q; a slice carries only files from within the addon subtree",
			value, root,
		)
	}
	return nil
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
// them deterministically and reports the same failure every run.
func sortedKeys(table map[string]string) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
