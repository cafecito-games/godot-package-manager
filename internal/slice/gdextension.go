package slice

import (
	"fmt"
	"sort"
	"strings"
)

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
// and the per-slice entries the index publishes. The returned core is the input
// with every partitioned section emptied and every other section — including
// sections gpm does not recognize — preserved verbatim, comments and ordering
// intact. The returned map groups the removed entries by the slice that owns the
// file each entry names, and then by the section it came from.
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
	document, err := parseGodotConfig(content)
	if err != nil {
		return nil, nil, manifestErrorf("partitioning .gdextension: %s", err)
	}
	if err := document.validatePartitionedSectionHeaders(); err != nil {
		return nil, nil, manifestErrorf("partitioning .gdextension: %s", err)
	}

	removed := map[SliceID]ExtensionEntries{}
	for _, section := range PartitionedSections() {
		block := document.block(section)
		if block == nil {
			continue
		}
		// Duplicates are tracked per section, because the same key in both sections
		// is the shape Godot expects rather than a mistake.
		seen := map[string]struct{}{}
		for _, entry := range block.entries {
			owner, err := ReduceLibraryKey(entry.key)
			if err != nil {
				// Reported by message rather than wrapped: ReduceLibraryKey already
				// returns a *output.ManifestError, and wrapping a typed error would let
				// the inner type decide the exit code of whatever wraps this one.
				return nil, nil, manifestErrorf("partitioning .gdextension [%s]: %s", section, err)
			}
			if _, duplicate := seen[entry.key]; duplicate {
				return nil, nil, manifestErrorf(
					"partitioning .gdextension [%s]: key %q is declared more than once",
					section, entry.key,
				)
			}
			seen[entry.key] = struct{}{}

			value, err := parseExtensionValue(entry.value)
			if err != nil {
				return nil, nil, manifestErrorf("partitioning .gdextension [%s] key %q: %s", section, entry.key, err)
			}
			if err := validatePartitionedValue(value); err != nil {
				return nil, nil, manifestErrorf("partitioning .gdextension [%s] key %q: %s", section, entry.key, err)
			}
			if err := requireWithinAddonRoot(root, value); err != nil {
				return nil, nil, manifestErrorf("partitioning .gdextension [%s] key %q: %s", section, entry.key, err)
			}

			entries, found := removed[owner]
			if !found {
				entries = ExtensionEntries{}
				removed[owner] = entries
			}
			if entries[section] == nil {
				entries[section] = map[string]string{}
			}
			entries[section][entry.key] = value
		}
	}
	return document.render(nil), removed, nil
}

// ReassembleExtension rebuilds an installed .gdextension from the core body and
// the entries of the slices that were installed. entries is the index's
// per-slice entries for this one .gdextension file; selected is the installed
// slice set. A slice present in entries but not selected contributes nothing,
// which is how a project that installed only its own platforms ends up with a
// .gdextension describing exactly the binaries on disk.
//
// Output is deterministic and idempotent: entries are emitted sorted by key
// within their section, and the sections themselves keep the position the author
// gave them in the core body rather than being reordered, so two machines
// installing the same slice set write identical bytes and a repeat install does
// not dirty the working tree. Validation walks PartitionedSections in order, so
// a file with problems in both sections always reports the same one.
//
// Every failure is an *output.FetchError: a core body and the entries beside it
// are remote content published by the addon's producer, and a mismatch between
// them is never a reason to install a .gdextension that misdescribes the tree.
// No filesystem access is performed.
func ReassembleExtension(core []byte, entries map[SliceID]ExtensionEntries, selected []SliceID) ([]byte, error) {
	document, err := parseGodotConfig(core)
	if err != nil {
		return nil, fetchErrorf("reassembling .gdextension: %s", err)
	}
	if err := document.validatePartitionedSectionHeaders(); err != nil {
		return nil, fetchErrorf("reassembling .gdextension: %s", err)
	}
	for _, section := range PartitionedSections() {
		if block := document.block(section); block != nil && len(block.entries) > 0 {
			return nil, fetchErrorf(
				"reassembling .gdextension: the core body still declares %d entries in [%s], so it was published without being partitioned",
				len(block.entries), section,
			)
		}
	}

	filled := map[ExtensionSection][]godotConfigEntry{}
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
				if err := appendReassembledEntry(document, filled, owners, section, id, key, sliceEntries[section][key]); err != nil {
					return nil, err
				}
			}
		}
	}

	for section, sectionEntries := range filled {
		sort.Slice(sectionEntries, func(left, right int) bool {
			return sectionEntries[left].key < sectionEntries[right].key
		})
		filled[section] = sectionEntries
	}
	return document.render(filled), nil
}

// appendReassembledEntry validates one index entry against the slice that
// declared it and places it in the section it belongs to.
func appendReassembledEntry(
	document *godotConfigDocument,
	filled map[ExtensionSection][]godotConfigEntry,
	owners map[ExtensionSection]map[string]SliceID,
	section ExtensionSection,
	id SliceID,
	key string,
	value string,
) error {
	owner, err := ReduceLibraryKey(key)
	if err != nil {
		// Reported by message: the error carries a manifest exit code that does not
		// apply to remote content.
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
	if document.block(section) == nil {
		return fetchErrorf(
			"reassembling .gdextension: slice %q declares a [%s] entry but the core body declares no [%s] section",
			id, section, section,
		)
	}
	owners[section][key] = id
	filled[section] = append(filled[section], godotConfigEntry{key: key, value: value})
	return nil
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

// parseExtensionValue reads a partitioned entry's value, which is one quoted
// res:// path and nothing else. A trailing comment is permitted, because authors
// write them.
//
// Godot also accepts a dictionary as a [dependencies] value, mapping each
// dependency to the subdirectory it is copied into on export. gpm's index
// publishes one path per key, so a dictionary is reported rather than flattened:
// flattening it would drop the destinations and install a .gdextension that no
// longer describes what Godot should copy.
func parseExtensionValue(raw string) (string, error) {
	if strings.HasPrefix(raw, "{") {
		return "", fmt.Errorf(
			"value %s is a Godot dictionary; a partitioned entry names exactly one quoted %s path",
			summarizeValue(raw), resourcePrefix,
		)
	}
	if !strings.HasPrefix(raw, `"`) {
		return "", fmt.Errorf(
			"value %s is not a quoted %s path",
			summarizeValue(raw), resourcePrefix,
		)
	}
	rest := raw[1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return "", fmt.Errorf("value %s is not a closed string", summarizeValue(raw))
	}
	if trailing := strings.TrimSpace(rest[end+1:]); trailing != "" && !isComment(trailing) {
		return "", fmt.Errorf(
			"value %s is followed by %q, which is neither whitespace nor a comment",
			summarizeValue(raw), trailing,
		)
	}
	return rest[:end], nil
}

// summarizeValue renders a value for a diagnostic, shortening a multi-line one to
// its first line so that a message stays one line.
func summarizeValue(raw string) string {
	first, _, multiline := strings.Cut(raw, lineFeed)
	if multiline {
		return fmt.Sprintf("%q...", strings.TrimSpace(first))
	}
	return fmt.Sprintf("%q", raw)
}

// validatePartitionedValue rejects an entry value that is not a clean res:// path
// inside the project, and that could not be written back out as a quoted string.
// It is applied on the way in during partition and again on the way out during
// reassembly, so an index assembled by hand cannot produce a .gdextension that no
// longer parses.
func validatePartitionedValue(value string) error {
	if err := validateResourcePath(value); err != nil {
		return err
	}
	if strings.ContainsAny(value, `"`) {
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
