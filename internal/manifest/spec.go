package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// SourceType identifies how an addon is obtained.
type SourceType string

const (
	SourceGit           SourceType = "git"
	SourceGitHubRelease SourceType = "github-release"
	SourceArchive       SourceType = "archive"
)

// AddonSpec is one addon entry declared in addons.toml.
type AddonSpec struct {
	// Name is the TOML table key. It is set during Load and is not a TOML field.
	Name string `toml:"-"`

	Source     SourceType `toml:"source"`
	URL        string     `toml:"url,omitempty"`
	Repo       string     `toml:"repo,omitempty"`
	Version    string     `toml:"version,omitempty"`
	Asset      string     `toml:"asset,omitempty"`
	SourcePath string     `toml:"source_path,omitempty"`
	InstallAs  string     `toml:"install_as,omitempty"`
	Exclude    []string   `toml:"exclude,omitempty"`
	// Platforms is the effective list of declared platform tags for this
	// addon, resolved by Load. A per-addon platforms list in addons.toml
	// REPLACES the project list for that addon; the two are never merged, so
	// an addon that declares its own platforms is unaffected by later
	// additions to [project] platforms. The implicit core and host slices are
	// never members of this list.
	Platforms []string `toml:"platforms,omitempty"`
	// Index, for an archive source, is the absolute URL of the addon's
	// gpm-index.toml, marking the archive as sliced.
	Index string `toml:"index,omitempty"`

	// platformsInherited records that Load filled Platforms from the project
	// table rather than from the addon's own table. Save consults it so a
	// load-modify-save cycle — which `gpm add`, `gpm remove`, and the AssetLib
	// wizard all perform — writes the addon table back as it was read. Without
	// it, the resolved project list would be written out as a per-addon key and
	// every addon would silently become a permanent override, deaf to later
	// edits of [project] platforms.
	platformsInherited bool
	// Checksum, when set, is the expected SHA-256 (64 lowercase hex digits) of
	// the downloaded archive or release asset. It is verified on every fetch,
	// including the first, for archive and github-release sources.
	Checksum string `toml:"checksum,omitempty"`
}

// ProjectConfig is the [project] table of addons.toml: settings that apply to
// the project as a whole rather than to one addon.
type ProjectConfig struct {
	// Platforms is the list of declared platform tags the project targets.
	// It is the default for every addon that does not declare its own.
	Platforms []string `toml:"platforms,omitempty"`
}

// Manifest is the parsed contents of addons.toml.
type Manifest struct {
	Project ProjectConfig        `toml:"project"`
	Addons  map[string]AddonSpec `toml:"addons"`
}

// InstallName returns the directory name under addons/ for this addon.
func (s AddonSpec) InstallName() string {
	if s.InstallAs != "" {
		return s.InstallAs
	}
	return s.Name
}

// fieldSeparator joins the top-level fields of the hashed representation. It
// differs from elementSeparator so that an element of a list field can never be
// mistaken for a field boundary.
const fieldSeparator = "\x01"

// elementSeparator joins the count prefix and the elements of one list field.
const elementSeparator = "\x00"

// Hash returns a stable hash of the spec's resolvable fields, used to detect
// drift between addons.toml and addons.lock.
//
// Encoding. Scalar fields contribute their value; a list field contributes its
// element count in decimal, then elementSeparator, then its elements joined
// with elementSeparator, after being sorted and deduplicated. The fields are
// joined with fieldSeparator. The count prefix is what makes the encoding
// unambiguous: without it, moving an element from one list to an adjacent one
// would leave the flattened bytes unchanged and drift would go undetected. A
// new field must follow this scheme, and a new list field must be
// length-prefixed like the existing ones.
//
// nil and an empty list encode identically, so "platforms absent" and
// "platforms = []" hash the same. The implicit core and host slices are never
// part of the representation, which is what keeps a spec_hash identical across
// machines; nothing here reads the host.
func (s AddonSpec) Hash() string {
	fields := []string{
		string(s.Source),
		s.URL,
		s.Repo,
		s.Version,
		s.Asset,
		s.SourcePath,
		s.InstallAs,
		s.Checksum,
		s.Index,
		encodeHashedList(s.Exclude),
		encodeHashedList(s.Platforms),
	}
	checksum := sha256.Sum256([]byte(strings.Join(fields, fieldSeparator)))
	return hex.EncodeToString(checksum[:])
}

// encodeHashedList renders one list field for Hash: a decimal element count,
// then the sorted, deduplicated elements. The count prefix is what keeps two
// adjacent list fields from borrowing elements from one another.
func encodeHashedList(values []string) string {
	normalized := append([]string(nil), values...)
	sort.Strings(normalized)
	normalized = slices.Compact(normalized)
	encoded := make([]string, 0, len(normalized)+1)
	encoded = append(encoded, strconv.Itoa(len(normalized)))
	encoded = append(encoded, normalized...)
	return strings.Join(encoded, elementSeparator)
}
