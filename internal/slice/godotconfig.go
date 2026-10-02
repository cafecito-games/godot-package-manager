package slice

import (
	"fmt"
	"slices"
	"strings"
)

// byteOrderMark is the UTF-8 byte order mark. Godot never writes one, but an
// author's editor may add it, and a file gpm rewrites must keep whatever it
// arrived with.
const byteOrderMark = "\ufeff"

// lineFeed and carriageReturnLineFeed are the two line endings a .gdextension
// reaches gpm with: a file authored on Windows is CRLF throughout and must stay
// that way, because rewriting its endings would show up as a whole-file diff in
// a project that vendors addons/ into git.
const (
	lineFeed               = "\n"
	carriageReturnLineFeed = "\r\n"
)

// godotConfigDocument is a parsed Godot ini-like configuration file, the format
// .gdextension uses: section headers in brackets, `key = value` statements, and
// comments introduced by ';' or '#'.
//
// This is the repository's only parser of that format. It is lossless for
// everything it does not rewrite: each line is retained with its own line
// ending, so a section nobody touches is emitted back byte for byte together
// with its comments and its ordering.
type godotConfigDocument struct {
	// byteOrderMark is the document's leading BOM, or the empty string.
	byteOrderMark string

	// lineEnding is the ending generated lines use: the first ending the document
	// itself uses, so a CRLF document stays CRLF.
	lineEnding string

	// blocks are the document's sections in file order, preceded by a block with
	// an empty name holding whatever came before the first section header.
	blocks []*godotConfigBlock
}

// godotConfigBlock is one section of a document, or the preamble ahead of the
// first section header.
type godotConfigBlock struct {
	// name is the section name, or the empty string for the preamble.
	name string

	// headerLine is the raw `[name]` line with its line ending, and is empty for
	// the preamble.
	headerLine string

	// bodyLines are the raw lines after the header, each with its own line ending,
	// so a block emitted verbatim reproduces its input exactly.
	bodyLines []string

	// entries are the body's statements in file order. A statement whose value
	// spans several lines appears once, with its lines joined by a line feed.
	entries []godotConfigEntry
}

// godotConfigEntry is one `key = value` statement. The value is the raw text to
// the right of the separator, unquoted and uninterpreted: what a value means
// depends on the section, and only the partitioned sections interpret theirs.
type godotConfigEntry struct {
	key   string
	value string
}

// parseGodotConfig parses configuration bytes. It operates purely on the bytes
// it is handed; the content of a .gdextension is read by its caller.
//
// Failures are returned untyped on purpose. The same malformed file is an
// author's mistake when it is being packaged and a producer's mistake when it is
// being installed, so the exit code belongs to the caller: output.CodeFor
// resolves a *output.ManifestError before a *output.FetchError, and an inner
// typed error would decide the exit code for whoever wrapped it.
func parseGodotConfig(content []byte) (*godotConfigDocument, error) {
	text := string(content)
	document := &godotConfigDocument{lineEnding: lineFeed}
	if trimmed, found := strings.CutPrefix(text, byteOrderMark); found {
		document.byteOrderMark = byteOrderMark
		text = trimmed
	}

	lines := splitKeepingLineEndings(text)
	for _, line := range lines {
		if ending := lineEndingOf(line); ending != "" {
			document.lineEnding = ending
			break
		}
	}

	current := &godotConfigBlock{}
	document.blocks = []*godotConfigBlock{current}

	for index := 0; index < len(lines); index++ {
		statement := strings.TrimSpace(trimLineEnding(lines[index]))
		switch {
		case statement == "" || isComment(statement):
			current.bodyLines = append(current.bodyLines, lines[index])
		case strings.HasPrefix(statement, "["):
			name, err := parseSectionHeader(statement, index)
			if err != nil {
				return nil, err
			}
			current = &godotConfigBlock{name: name, headerLine: lines[index]}
			document.blocks = append(document.blocks, current)
		default:
			entry, consumed, err := parseGodotConfigEntry(lines, index)
			if err != nil {
				return nil, err
			}
			current.entries = append(current.entries, entry)
			current.bodyLines = append(current.bodyLines, lines[index:index+consumed]...)
			index += consumed - 1
		}
	}
	return document, nil
}

// block returns the document's block for a partitioned section, or nil when the
// document declares no such section.
func (document *godotConfigDocument) block(section ExtensionSection) *godotConfigBlock {
	for _, candidate := range document.blocks {
		if candidate.name == string(section) {
			return candidate
		}
	}
	return nil
}

// validatePartitionedSectionHeaders rejects the two header shapes that would make
// a partition lose entries rather than move them.
//
// A partitioned section declared twice is ambiguous: Godot merges repeated
// sections, but emptying one header and leaving the other would silently drop
// entries, so the ambiguity is reported instead of resolved.
//
// A header differing from a partitioned section only in case is rejected rather
// than treated as an unrecognized section. Godot's section names are
// case-sensitive, so such a section is already broken; treating it as unknown
// would preserve it verbatim and copy its platform-tagged entries straight into
// the core body, where they would name binaries no slice installs.
func (document *godotConfigDocument) validatePartitionedSectionHeaders() error {
	seen := map[string]struct{}{}
	for _, block := range document.blocks {
		if block.name == "" {
			continue
		}
		if !slices.Contains(partitionedSections, ExtensionSection(block.name)) {
			for _, section := range partitionedSections {
				if strings.EqualFold(block.name, string(section)) {
					return fmt.Errorf(
						"section [%s] differs from [%s] only in case, and section names are case-sensitive",
						block.name, section,
					)
				}
			}
			continue
		}
		if _, found := seen[block.name]; found {
			return fmt.Errorf("section [%s] is declared more than once", block.name)
		}
		seen[block.name] = struct{}{}
	}
	return nil
}

// render writes the document back out, replacing the body of every partitioned
// section with the entries supplied for it. Passing no entries empties those
// sections, which is what partition emits as the core body; passing a section's
// entries fills it, which is what reassembly emits.
//
// Every other block, the preamble included, is written byte for byte. Generated
// lines use the document's own line ending, and a partitioned section is always
// followed by one blank line when another block follows it, so partitioning a
// reassembled file reproduces the core body it was reassembled from.
func (document *godotConfigDocument) render(filled map[ExtensionSection][]godotConfigEntry) []byte {
	var builder strings.Builder
	builder.WriteString(document.byteOrderMark)

	for index, block := range document.blocks {
		section := ExtensionSection(block.name)
		if !slices.Contains(partitionedSections, section) {
			builder.WriteString(block.headerLine)
			for _, line := range block.bodyLines {
				builder.WriteString(line)
			}
			continue
		}

		// The header keeps its own ending where it has one. A header that ended the
		// file without a newline still needs one here, because the section's
		// entries are written on the lines after it.
		builder.WriteString(trimLineEnding(block.headerLine))
		builder.WriteString(document.lineEnding)

		if entries := filled[section]; len(entries) > 0 {
			builder.WriteString(document.lineEnding)
			for _, entry := range entries {
				builder.WriteString(entry.key)
				builder.WriteString(" = ")
				builder.WriteString(`"` + entry.value + `"`)
				builder.WriteString(document.lineEnding)
			}
		}
		if index < len(document.blocks)-1 {
			builder.WriteString(document.lineEnding)
		}
	}
	return []byte(builder.String())
}

// parseSectionHeader parses a `[name]` line. index is zero-based and is reported
// one-based, so a diagnostic points at the line an editor shows.
func parseSectionHeader(statement string, index int) (string, error) {
	if !strings.HasSuffix(statement, "]") {
		return "", fmt.Errorf("line %d: section header %q is not closed with %q", index+1, statement, "]")
	}
	name := strings.TrimSpace(statement[1 : len(statement)-1])
	if name == "" {
		return "", fmt.Errorf("line %d: section header %q names no section", index+1, statement)
	}
	if strings.ContainsAny(name, "[]") {
		return "", fmt.Errorf("line %d: section header %q is malformed", index+1, statement)
	}
	return name, nil
}

// parseGodotConfigEntry parses the statement starting at lines[start] and reports
// how many lines it spans. Godot values may be dictionaries or arrays written
// across several lines, so the value runs until its brackets balance outside of
// a string.
func parseGodotConfigEntry(lines []string, start int) (godotConfigEntry, int, error) {
	key, value, found := strings.Cut(trimLineEnding(lines[start]), "=")
	if !found {
		return godotConfigEntry{}, 0, fmt.Errorf(
			"line %d: %q is neither a section header, a comment, nor a key = value statement",
			start+1, strings.TrimSpace(trimLineEnding(lines[start])),
		)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return godotConfigEntry{}, 0, fmt.Errorf("line %d: statement names no key", start+1)
	}
	if strings.ContainsAny(key, `"[]`) {
		return godotConfigEntry{}, 0, fmt.Errorf("line %d: key %q is malformed", start+1, key)
	}

	scan := valueScan{}
	scan.consume(value)
	consumed := 1
	for scan.incomplete() {
		if start+consumed >= len(lines) {
			return godotConfigEntry{}, 0, fmt.Errorf("line %d: the value of %q is never closed", start+1, key)
		}
		continuation := trimLineEnding(lines[start+consumed])
		value += lineFeed + continuation
		scan.consume(continuation)
		consumed++
	}
	return godotConfigEntry{key: key, value: strings.TrimSpace(value)}, consumed, nil
}

// valueScan tracks whether a value has ended: it has, once every bracket it
// opened is closed and it is not inside a string.
type valueScan struct {
	depth    int
	inString bool
	escaped  bool
}

func (scan *valueScan) consume(text string) {
	for _, character := range text {
		switch {
		case scan.escaped:
			scan.escaped = false
		case scan.inString && character == '\\':
			scan.escaped = true
		case character == '"':
			scan.inString = !scan.inString
		case scan.inString:
		case character == '{' || character == '[' || character == '(':
			scan.depth++
		case character == '}' || character == ']' || character == ')':
			if scan.depth > 0 {
				scan.depth--
			}
		}
	}
	// A backslash at the end of a line escapes nothing on the next one in Godot's
	// config format, so the escape never carries across a line.
	scan.escaped = false
}

func (scan *valueScan) incomplete() bool { return scan.depth > 0 || scan.inString }

// isComment reports whether a trimmed line is a comment. Godot's config format
// accepts both introducers, and real .gdextension files use both.
func isComment(statement string) bool {
	return strings.HasPrefix(statement, ";") || strings.HasPrefix(statement, "#")
}

// splitKeepingLineEndings splits text into lines, each keeping its own ending, so
// that a document mixing endings is reproduced as it arrived. A final line
// without an ending is returned without one.
func splitKeepingLineEndings(text string) []string {
	var lines []string
	for len(text) > 0 {
		index := strings.Index(text, lineFeed)
		if index < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:index+1])
		text = text[index+1:]
	}
	return lines
}

// lineEndingOf returns the line's ending, or the empty string for a final line
// that has none.
func lineEndingOf(line string) string {
	switch {
	case strings.HasSuffix(line, carriageReturnLineFeed):
		return carriageReturnLineFeed
	case strings.HasSuffix(line, lineFeed):
		return lineFeed
	default:
		return ""
	}
}

// trimLineEnding returns the line without its ending.
func trimLineEnding(line string) string {
	return strings.TrimSuffix(line, lineEndingOf(line))
}
