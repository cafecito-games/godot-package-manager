package slice

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// SupportedIndexFormat is the highest gpm-index.toml format this gpm
// understands. It is the single declaration of that maximum: an index declaring
// a higher format is rejected outright rather than interpreted in part, because
// a future format may give an existing key a new meaning.
const SupportedIndexFormat = 1

// resourcePrefix is the Godot resource scheme every partitioned entry value
// starts with. An entry value is resolved against the installed project, so a
// value outside res:// is never valid.
const resourcePrefix = "res://"

// extensionSuffix is the file extension of a Godot extension descriptor, which
// is what a partitioned section's path key names.
const extensionSuffix = ".gdextension"

// Index is a parsed gpm-index.toml: the contract an addon's producer publishes
// beside its slice archives. It is the only declaration of that schema; no other
// package parses gpm-index.toml.
type Index struct {
	// Format is the index format version. It is written and validated first so
	// that an index from a newer gpm is rejected before any other field is read.
	Format int `toml:"format"`

	Name    string `toml:"name"`
	Version string `toml:"version"`

	// Slices maps a slice ID in its canonical tag form to the archive publishing
	// it. The key is parsed with ParseSliceID, so "core" is accepted here.
	Slices map[string]*IndexSlice `toml:"slices"`
}

// IndexSlice is one published slice: the archive that carries it, and the
// .gdextension entries that were partitioned out of the addon tree because they
// name files only this slice ships.
type IndexSlice struct {
	// File is the archive's asset name, a bare file name that the consumer joins
	// to the location it downloads from.
	File string `toml:"file"`

	// SHA256 is the archive's SHA-256 digest as 64 lowercase hex digits.
	SHA256 string `toml:"sha256"`

	// Size is the archive's size in bytes.
	Size int64 `toml:"size"`

	// Libraries and Dependencies are the partitioned .gdextension sections, each
	// mapping a .gdextension path relative to the addon root to that section's
	// platform-tagged entries. A Godot platform tag legitimately appears in
	// both — one names the library binary, the other the files shipped beside
	// it — so they are siblings sharing every structural level. They differ only
	// in the leaf: a [libraries] entry names one res:// path, while a
	// [dependencies] entry is Godot's Dictionary of dependency paths to export
	// destinations.
	//
	// Their declaration order is the order of PartitionedSections, which is also
	// the order they are written and validated in.
	Libraries    ExtensionSectionTable[string]                     `toml:"libraries,omitempty"`
	Dependencies ExtensionSectionTable[ExtensionDependencyTargets] `toml:"dependencies,omitempty"`
}

// ExtensionEntryTable is one .gdextension section's platform-tagged entries for
// one file: a Godot platform tag mapped to that entry's value.
type ExtensionEntryTable[Value any] map[string]Value

// ExtensionSectionTable is one slice's table for one partitioned .gdextension
// section: a .gdextension path relative to the addon root, then that section's
// platform-tagged entries. It is generic in the value because the two
// partitioned sections do not share a value type — a [libraries] entry names one
// res:// path, while a [dependencies] entry is Godot's Dictionary — while every
// level above the value obeys identical rules.
type ExtensionSectionTable[Value any] map[string]ExtensionEntryTable[Value]

// ExtensionDependencyTargets is one [dependencies] entry's Godot Dictionary:
// each dependency's res:// path mapped to the export subdirectory Godot copies
// it into. An empty destination means the dependency is copied beside the
// exported binary, which is what every published addon writes today.
//
// This is the single declaration of that shape. Neither ExtensionEntries nor
// IndexSlice spells the underlying map again.
type ExtensionDependencyTargets map[string]string

// ExtensionPaths returns the .gdextension path keys the named section declares,
// in ascending order, and nil for a section this schema does not partition. It
// is the structural accessor for a caller that walks PartitionedSections and
// needs to know which files a slice ships; the entries themselves are reached
// through the statically typed fields, because a Go method cannot be generic and
// a single accessor would have to widen one section's value back to a lossy
// type.
func (indexSlice *IndexSlice) ExtensionPaths(section ExtensionSection) []string {
	// Keyed by section rather than switched on, so a section added to the
	// vocabulary without a field here resolves to nil in exactly one place.
	resolvers := map[ExtensionSection]func() []string{
		SectionLibraries:    func() []string { return sortedKeys(indexSlice.Libraries) },
		SectionDependencies: func() []string { return sortedKeys(indexSlice.Dependencies) },
	}
	resolve, declared := resolvers[section]
	if !declared {
		return nil
	}
	paths := resolve()
	if len(paths) == 0 {
		return nil
	}
	return paths
}

// sha256Pattern matches a bare SHA-256 digest: exactly 64 lowercase hex digits.
// It is the repository's only SHA-256 format pattern, and it lives here because
// this is the lowest layer that validates a digest: internal/slice depends only
// on the standard library, the TOML library, and internal/output, so every
// higher layer can call into it without an import cycle.
var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidateChecksum rejects checksum values that are not a bare SHA-256 digest.
func ValidateChecksum(checksum string) error {
	if !sha256Pattern.MatchString(checksum) {
		return fmt.Errorf("checksum %q must be 64 lowercase hex digits (SHA-256)", checksum)
	}
	return nil
}

// IndexChecksum returns the SHA-256 digest of raw index bytes as lowercase hex.
// It is computed over the bytes as downloaded, before parsing, so the digest
// recorded in a lockfile pins the exact document and a change that alters
// parsing cannot evade the pin.
func IndexChecksum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// LoadIndex parses and fully validates index bytes. It takes bytes rather than a
// path or a URL because the index arrives over the network, which also makes it
// testable without disk or network access.
//
// Every failure is an *output.FetchError: the index is remote content, and a
// malformed index is never a reason to fall back to an unsliced install. A
// rejected index yields a nil *Index, so no partially interpreted index is ever
// reachable by a caller.
func LoadIndex(data []byte) (*Index, error) {
	// format is decoded on its own first. Decoding the whole schema up front
	// would turn a newer index whose known keys changed type into a generic
	// decoding failure, hiding the one diagnosis that helps: the format is newer
	// than this gpm. A probe that declares only format leaves every other key
	// undecoded, so no other key is type-checked yet.
	var probe struct {
		Format int `toml:"format"`
	}
	probeMetaData, err := toml.Decode(string(data), &probe)
	if err != nil {
		return nil, fetchErrorf("parsing index: %w", err)
	}
	if err := validateIndexFormat(probe.Format, probeMetaData); err != nil {
		return nil, err
	}

	index := &Index{}
	metaData, err := toml.Decode(string(data), index)
	if err != nil {
		return nil, fetchErrorf("parsing index: %w", err)
	}
	if err := rejectUnknownIndexKeys(metaData); err != nil {
		return nil, err
	}
	if err := index.validate(); err != nil {
		return nil, err
	}
	return index, nil
}

// validateIndexFormat checks format before any other field is interpreted. The
// field is mandatory rather than defaulted, because an index that forgot it is a
// producer bug and guessing format 1 would hide it.
func validateIndexFormat(format int, metaData toml.MetaData) error {
	if !metaData.IsDefined("format") {
		return fetchErrorf("index declares no format; format is mandatory and is not defaulted")
	}
	if format < 1 {
		return fetchErrorf("invalid index format %d: format must be a positive integer", format)
	}
	if format > SupportedIndexFormat {
		return fetchErrorf(
			"unsupported index format %d: this gpm understands index format %d at most, so upgrade gpm to install this addon",
			format, SupportedIndexFormat,
		)
	}
	return nil
}

// rejectUnknownIndexKeys fails on any key the schema does not declare. Strict
// decoding is the point: a key silently ignored today could carry meaning in a
// future format, and ignoring it would misread the index rather than report it.
func rejectUnknownIndexKeys(metaData toml.MetaData) error {
	// The TOML library matches a struct field case-insensitively when no exact
	// match exists, and records the document's own spelling as decoded. TOML keys
	// are case-sensitive, so a key that differs only in case is an unknown key
	// that strict decoding alone would let through.
	for _, key := range metaData.Keys() {
		if err := rejectMisspelledIndexKey(metaData, key); err != nil {
			return err
		}
	}

	undecoded := metaData.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	// The smallest key is reported so that an index with several unknown keys
	// always produces the same message.
	smallest := undecoded[0]
	for _, key := range undecoded[1:] {
		if strings.Join(key, ".") < strings.Join(smallest, ".") {
			smallest = key
		}
	}
	if len(smallest) >= 3 && smallest[0] == "slices" {
		return unknownSliceKeyError(strings.Join(smallest[2:], "."), smallest[1])
	}
	return unknownIndexKeyError(strings.Join(smallest, "."))
}

// topLevelIndexKeys is the exact spelling of every key the index declares at the
// top level.
var topLevelIndexKeys = []string{"format", "name", "version", "slices"}

// sliceFieldNames returns the exact spelling of every key a slice table declares,
// reading the partitioned sections from their single declaration.
func sliceFieldNames() []string {
	names := []string{"file", "sha256", "size"}
	for _, section := range PartitionedSections() {
		names = append(names, string(section))
	}
	return names
}

// rejectMisspelledIndexKey checks one document key against the schema at the
// position it appears in. Positions the schema leaves open — a slice ID, a
// .gdextension path, a platform tag — are validated later as values, not here.
func rejectMisspelledIndexKey(metaData toml.MetaData, key toml.Key) error {
	if len(key) == 0 {
		return nil
	}
	if !slices.Contains(topLevelIndexKeys, key[0]) {
		return unknownIndexKeyError(strings.Join(key, "."))
	}
	if key[0] != "slices" {
		if len(key) > 1 {
			return unknownIndexKeyError(strings.Join(key, "."))
		}
		return nil
	}
	if len(key) >= 3 && !slices.Contains(sliceFieldNames(), key[2]) {
		return unknownSliceKeyError(strings.Join(key[2:], "."), key[1])
	}
	if len(key) >= 4 {
		// A key below a slice's own fields is inside a partitioned section, and
		// how deep that section nests is the section's own property.
		maximumDepth, partitioned := indexSectionKeyDepth(ExtensionSection(key[2]))
		if !partitioned || len(key) > maximumDepth {
			return unknownSliceKeyError(strings.Join(key[2:], "."), key[1])
		}
		if err := requireIndexEntryValueType(metaData, key); err != nil {
			return err
		}
	}
	return nil
}

// indexEntryKeyDepth is the number of index key components an entry key has:
// slices.<id>.<section>.<path>.<entryKey>.
const indexEntryKeyDepth = 5

// indexSectionEntryIsTable declares, for each partitioned section, whether an
// entry's value is a TOML table. It is the single declaration of the one way the
// two sections differ — a [libraries] entry names one res:// path, while a
// [dependencies] entry is Godot's Dictionary of dependency paths to export
// destinations — and both the section's maximum key depth and the TOML type its
// entries must be written in are derived from it.
var indexSectionEntryIsTable = map[ExtensionSection]bool{
	SectionLibraries:    false,
	SectionDependencies: true,
}

// indexSectionKeyDepth returns the number of index key components the named
// section's deepest key has, and whether the section is partitioned at all. A
// section whose entries are tables reaches one level past an entry key, because
// the table's own keys are index keys too.
func indexSectionKeyDepth(section ExtensionSection) (int, bool) {
	entryIsTable, partitioned := indexSectionEntryIsTable[section]
	if !partitioned {
		return 0, false
	}
	if entryIsTable {
		return indexEntryKeyDepth + 1, true
	}
	return indexEntryKeyDepth, true
}

// requireIndexEntryValueType rejects an entry written in a TOML type the section
// does not declare.
//
// Strict decoding does not cover this on its own. It reports a table where the
// schema declares a string, but it decodes a string into an empty map where the
// schema declares a table, recording neither an error nor an undecoded key. An
// index still written in the pre-nesting shape — a bare res:// string as a
// [dependencies] entry — would therefore reach validation as an entry naming no
// dependency, rejected but misdiagnosed, and a producer would be told their
// table is empty rather than that their shape is a format older than this one.
func requireIndexEntryValueType(metaData toml.MetaData, key toml.Key) error {
	if len(key) != indexEntryKeyDepth {
		return nil
	}
	entryIsTable, partitioned := indexSectionEntryIsTable[ExtensionSection(key[2])]
	if !partitioned {
		return nil
	}
	const tomlTableType = "Hash"
	declaredType := metaData.Type(key...)
	if entryIsTable == (declaredType == tomlTableType) {
		return nil
	}
	expected := "one quoted string"
	if entryIsTable {
		expected = "a table of " + resourcePrefix + " paths to export destinations"
	}
	return fetchErrorf(
		"index key %q is written as a TOML %s, but a %s entry is %s",
		strings.Join(key, "."), declaredType, key[2], expected,
	)
}

func unknownIndexKeyError(key string) error {
	return fetchErrorf(
		"unknown key %q in index: format %d understands format, name, version, and [slices]",
		key, SupportedIndexFormat,
	)
}

func unknownSliceKeyError(key, sliceKey string) error {
	return fetchErrorf(
		"unknown key %q in index slice %q: format %d understands file, sha256, size, and the partitioned tables %s",
		key, sliceKey, SupportedIndexFormat, describeSections(),
	)
}

// describeSections renders the partitioned-section vocabulary for a diagnostic,
// reading it from its single declaration rather than naming the sections again.
func describeSections() string {
	names := make([]string, 0, len(partitionedSections))
	for _, section := range PartitionedSections() {
		names = append(names, string(section))
	}
	return strings.Join(names, " and ")
}

// validate checks every field of a decoded index. Slice keys are visited in
// sorted order so that an index with several problems always reports the same
// one.
func (index *Index) validate() error {
	if index.Name == "" {
		return fetchErrorf("index declares no name")
	}
	if index.Version == "" {
		return fetchErrorf("index declares no version")
	}
	if len(index.Slices) == 0 {
		return fetchErrorf("index declares no [slices]; an index publishes at least the %q slice", CorePlatform)
	}

	keys := make([]string, 0, len(index.Slices))
	for key := range index.Slices {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parsed := make(map[string]SliceID, len(keys))
	coreFound := false
	for _, key := range keys {
		id, err := ParseSliceID(key)
		if err != nil {
			// The parse error is reported by message rather than wrapped: it is an
			// *output.ManifestError, and wrapping it would make output.CodeFor
			// report a manifest failure for what is remote content.
			return fetchErrorf("invalid index slice key %q: %s", key, err)
		}
		parsed[key] = id
		if id.IsCore() {
			coreFound = true
		}
	}
	if !coreFound {
		return fetchErrorf("index publishes no %q slice, which is mandatory because every project needs it", CorePlatform)
	}

	declaredFiles := make(map[string]string, len(keys))
	for _, key := range keys {
		indexSlice := index.Slices[key]
		if indexSlice == nil {
			return fetchErrorf("index slice %q declares no fields", key)
		}
		if err := validateIndexSlice(key, parsed[key], indexSlice); err != nil {
			return err
		}
		if owner, found := declaredFiles[indexSlice.File]; found {
			return fetchErrorf(
				"index slices %q and %q both declare file %q; one archive cannot be two slices",
				owner, key, indexSlice.File,
			)
		}
		declaredFiles[indexSlice.File] = key
	}
	return nil
}

func validateIndexSlice(key string, id SliceID, indexSlice *IndexSlice) error {
	if err := validateArchiveFileName(indexSlice.File); err != nil {
		return fetchErrorf("index slice %q: %s", key, err)
	}
	if err := ValidateChecksum(indexSlice.SHA256); err != nil {
		return fetchErrorf("index slice %q: invalid sha256: %s", key, err)
	}
	if indexSlice.Size <= 0 {
		return fetchErrorf("index slice %q: size %d must be a positive byte count", key, indexSlice.Size)
	}
	// One validator per partitioned section, each binding that section's table
	// and the leaf rule its values obey; every structural rule above the leaf is
	// shared and lives in validateSectionTable. They are keyed by section and
	// run in PartitionedSections order, so a slice with problems in both
	// sections always reports the [libraries] one, and a section added to the
	// vocabulary without a validator here is reported rather than left unchecked.
	validators := map[ExtensionSection]func() error{
		SectionLibraries: func() error {
			return validateSectionTable(key, id, SectionLibraries, indexSlice.Libraries, validateResourcePath)
		},
		SectionDependencies: func() error {
			return validateSectionTable(key, id, SectionDependencies, indexSlice.Dependencies, validateDependencyTargets)
		},
	}
	for _, section := range PartitionedSections() {
		validate, declared := validators[section]
		if !declared {
			return fetchErrorf("index slice %q: partitioned section %s is not validated by this gpm", key, section)
		}
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateArchiveFileName rejects anything but a bare asset name. The value is
// later joined to a download location and to a staging directory, so a separator
// or a traversal in it would reach outside both.
func validateArchiveFileName(file string) error {
	if file == "" {
		return fmt.Errorf("file must not be empty")
	}
	if strings.ContainsAny(file, `/\`) {
		return fmt.Errorf("file %q must not contain path separators", file)
	}
	if strings.Contains(file, ":") {
		return fmt.Errorf("file %q must be a bare asset name, not a path", file)
	}
	if file == "." || file == ".." {
		return fmt.Errorf("file %q is not an asset name", file)
	}
	if strings.ContainsFunc(file, func(character rune) bool { return character < ' ' || character == 0x7f }) {
		return fmt.Errorf("file name contains a control character")
	}
	return nil
}

// validateSectionTable validates one partitioned section of one slice. Every
// structural rule — a path key names a .gdextension file inside the addon, a
// table declared but empty is rejected, an entry key reduces to a slice the
// table belongs to, keys are visited in sorted order — is identical for every
// section, so it is written once here and the section's own leaf rule arrives as
// validateValue.
func validateSectionTable[Value any](
	key string,
	id SliceID,
	section ExtensionSection,
	table ExtensionSectionTable[Value],
	validateValue func(Value) error,
) error {
	if table == nil {
		return nil
	}
	// An empty table is distinguished from an absent one: the writer omits an
	// absent section, so accepting a declared-but-empty one would make
	// load, save, load return an index that is not equal to the one accepted.
	if len(table) == 0 {
		return fetchErrorf(
			"index slice %q declares an empty %s table; a section with no entries is omitted rather than declared",
			key, section,
		)
	}
	if id.IsCore() {
		return fetchErrorf(
			"index slice %q declares %s entries; platform-tagged entries belong to platform slices, and the %q slice carries none",
			key, section, CorePlatform,
		)
	}

	pathKeys := make([]string, 0, len(table))
	for pathKey := range table {
		pathKeys = append(pathKeys, pathKey)
	}
	sort.Strings(pathKeys)

	for _, pathKey := range pathKeys {
		if err := validateExtensionPath(pathKey); err != nil {
			return fetchErrorf("index slice %q: invalid %s path: %s", key, section, err)
		}
		entries := table[pathKey]
		if len(entries) == 0 {
			return fetchErrorf(
				"index slice %q declares %s for %q with no entries; an empty table is a producer mistake and would break byte-stable output",
				key, section, pathKey,
			)
		}
		entryKeys := make([]string, 0, len(entries))
		for entryKey := range entries {
			entryKeys = append(entryKeys, entryKey)
		}
		sort.Strings(entryKeys)
		for _, entryKey := range entryKeys {
			if err := validateSectionEntry(key, id, section, pathKey, entryKey, entries[entryKey], validateValue); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSectionEntry[Value any](
	key string,
	id SliceID,
	section ExtensionSection,
	pathKey, entryKey string,
	value Value,
	validateValue func(Value) error,
) error {
	owner, err := ReduceLibraryKey(entryKey)
	if err != nil {
		// Reported by message for the same reason as an invalid slice key: the
		// underlying error carries a manifest exit code that does not apply here.
		return fetchErrorf("index slice %q: invalid %s key in %q: %s", key, section, pathKey, err)
	}
	// A key with no architecture belongs to every architecture of its platform,
	// which is how Godot writes iOS and macOS keys, so it is accepted in an
	// architecture-specific slice. A key naming a different platform, or a
	// different architecture, was partitioned into the wrong slice.
	owned := owner.Platform == id.Platform && (owner.Architecture == "" || owner.Architecture == id.Architecture)
	if !owned {
		return fetchErrorf(
			"index slice %q: %s key %q in %q belongs to slice %q, not %q",
			key, section, entryKey, pathKey, owner, id,
		)
	}
	if err := validateValue(value); err != nil {
		return fetchErrorf("index slice %q: invalid %s entry %q in %q: %s", key, section, entryKey, pathKey, err)
	}
	return nil
}

// validateDependencyTargets is the [dependencies] leaf rule: one Godot
// Dictionary of dependency res:// paths to export destinations. The paths are
// visited in sorted order, so an entry with several problems always reports the
// same one.
//
// An empty Dictionary is rejected for the same reason a declared-but-empty
// section table is: the writer's omitempty does not reach this depth, so
// accepting one would publish a table header naming no dependency and make
// load, save, load disagree with the index that was accepted.
func validateDependencyTargets(targets ExtensionDependencyTargets) error {
	if len(targets) == 0 {
		return fmt.Errorf("the dependency table is empty; an entry naming no dependency is a producer mistake and would break byte-stable output")
	}
	for _, path := range sortedKeys(targets) {
		if err := validateResourcePath(path); err != nil {
			return fmt.Errorf("dependency path %s", err)
		}
		if err := validateExportDestination(targets[path]); err != nil {
			return fmt.Errorf("dependency %q: %s", path, err)
		}
	}
	return nil
}

// validateExportDestination rejects an export destination that is not a clean
// relative subdirectory. Godot copies the dependency into this subdirectory of
// the exported project, so an absolute path, a traversal, or a Windows
// separator in it writes outside the export directory.
//
// It is the single declaration of that rule, applied while partitioning an
// author's .gdextension, while loading a producer's index, and again while
// reassembling an installed .gdextension.
//
// The empty string is accepted and is the overwhelmingly common real case: it
// means the dependency is copied beside the exported binary.
func validateExportDestination(destination string) error {
	if destination == "" {
		return nil
	}
	if strings.HasPrefix(destination, resourcePrefix) {
		return fmt.Errorf(
			"destination %q must be a path relative to the export directory, not a %s path",
			destination, resourcePrefix,
		)
	}
	if err := validateRelativePath(destination); err != nil {
		return fmt.Errorf("destination %s", err)
	}
	if strings.Contains(destination, `"`) {
		return fmt.Errorf("destination %q must not contain a quote", destination)
	}
	if strings.ContainsFunc(destination, func(character rune) bool { return character < ' ' || character == 0x7f }) {
		return fmt.Errorf("destination contains a control character")
	}
	return nil
}

// validateExtensionPath rejects a section path key that does not name a
// .gdextension file inside the addon root. The key is later used to locate a
// file in the installed tree, so a traversal in it would reach outside the addon.
func validateExtensionPath(pathKey string) error {
	if err := validateRelativePath(pathKey); err != nil {
		return err
	}
	if !strings.HasSuffix(pathKey, extensionSuffix) || pathKey == extensionSuffix {
		return fmt.Errorf("%q must name a %s file", pathKey, extensionSuffix)
	}
	return nil
}

// validateResourcePath rejects an entry value that is not a res:// path inside
// the project. Godot resolves these against the project root, so a value that
// escapes it points outside the installed addon.
func validateResourcePath(value string) error {
	if !strings.HasPrefix(value, resourcePrefix) {
		return fmt.Errorf("%q must be a %s path", value, resourcePrefix)
	}
	relative := strings.TrimPrefix(value, resourcePrefix)
	if relative == "" {
		return fmt.Errorf("%q names no file under %s", value, resourcePrefix)
	}
	if err := validateRelativePath(relative); err != nil {
		return fmt.Errorf("%s: %w", value, err)
	}
	return nil
}

// validateRelativePath rejects every shape that is not a clean relative POSIX
// path: an absolute path, a Windows path, an empty or dotted component, and a
// traversal. Requiring the path to be clean also means two keys cannot spell the
// same file two ways.
func validateRelativePath(value string) error {
	if value == "" {
		return fmt.Errorf("path must not be empty")
	}
	if strings.Contains(value, `\`) {
		return fmt.Errorf("path %q must use %q separators", value, "/")
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, ":") {
		return fmt.Errorf("path %q must be relative", value)
	}
	for _, component := range strings.Split(value, "/") {
		switch component {
		case "":
			return fmt.Errorf("path %q has an empty component", value)
		case ".":
			return fmt.Errorf("path %q is not in its simplest form", value)
		case "..":
			return fmt.Errorf("path %q must not escape the addon root", value)
		}
	}
	return nil
}

// PublishedSliceIDs returns the published slices as parsed slice IDs, core first
// and the rest sorted, which is the form SelectSlices takes as its published
// set. It is the bridge between the index and the slice vocabulary: the
// vocabulary never imports the index type.
//
// A key that does not parse is skipped. LoadIndex rejects such a key, so this
// can only happen for an Index a producer assembled in memory.
func (index *Index) PublishedSliceIDs() []SliceID {
	set := make(map[SliceID]struct{}, len(index.Slices))
	for key := range index.Slices {
		id, err := ParseSliceID(key)
		if err != nil {
			continue
		}
		set[id] = struct{}{}
	}
	return sortedSliceIDs(set)
}

// Save writes the index to path as TOML through a temp file and a rename, so a
// mid-write failure never leaves a truncated index at path.
//
// Output is deterministic at every level: the top-level fields and then the
// partitioned tables in struct-declaration order, and slice keys,
// .gdextension path keys, entry keys, and a [dependencies] entry's dependency
// paths all in ascending order. A producer re-run over unchanged input
// therefore writes an identical file with an identical checksum.
func (index *Index) Save(path string) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".gpm-index-*.tmp")
	if err != nil {
		return &output.ManifestError{Err: fmt.Errorf("creating temp index: %w", err)}
	}
	temporaryName := temporary.Name()

	if err := toml.NewEncoder(temporary).Encode(index); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
		return &output.ManifestError{Err: fmt.Errorf("encoding index %s: %w", path, err)}
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
		return &output.ManifestError{Err: fmt.Errorf("syncing index %s: %w", path, err)}
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryName)
		return &output.ManifestError{Err: fmt.Errorf("closing index %s: %w", path, err)}
	}
	if err := os.Rename(temporaryName, path); err != nil {
		_ = os.Remove(temporaryName)
		return &output.ManifestError{Err: fmt.Errorf("installing index %s: %w", path, err)}
	}
	return nil
}
