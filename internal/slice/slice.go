// Package slice owns the Godot platform vocabulary the addon platform slices
// feature keys off: what a platform tag is, which tags are valid, and which
// slices a project on a given host needs. It is pure logic with no network and
// no disk access, and it is the only place in the tree that names a platform.
package slice

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// CorePlatform is the slice ID of the platform-independent slice. It is a
// mandatory key of every published index and may not be declared in a manifest,
// because every project needs it.
const CorePlatform = "core"

// componentSeparator separates the components of a Godot platform tag.
const componentSeparator = "."

// knownPlatforms is the single declaration of the Godot platforms gpm
// understands. No other package may hard-code a platform name.
var knownPlatforms = []string{"android", "ios", "linux", "macos", "web", "windows"}

// knownArchitectures is the single declaration of the Godot architecture
// components a slice ID may carry.
var knownArchitectures = []string{"arm32", "arm64", "rv64", "universal", "wasm32", "x86_32", "x86_64"}

// knownBuildTargets is the single declaration of the Godot build targets a
// .gdextension library key may carry. No build target ever appears in a slice
// ID; the set exists only so that reducing a library key can tell a droppable
// build-target component apart from an unknown one. It covers both spellings
// Godot .gdextension files use in practice: the current editor/template_debug/
// template_release keys and the shorter debug/release forms.
var knownBuildTargets = []string{"debug", "editor", "release", "template_debug", "template_release"}

// KnownPlatforms returns the known Godot platforms in sorted order.
func KnownPlatforms() []string { return slices.Clone(knownPlatforms) }

// KnownArchitectures returns the known Godot architectures in sorted order.
func KnownArchitectures() []string { return slices.Clone(knownArchitectures) }

// KnownBuildTargets returns the known Godot build targets in sorted order.
func KnownBuildTargets() []string { return slices.Clone(knownBuildTargets) }

// SliceID identifies one published slice of an addon: a Godot platform with an
// optional architecture, or the platform-independent core slice.
type SliceID struct {
	Platform     string
	Architecture string
}

// CoreSliceID returns the slice ID of the platform-independent core slice.
func CoreSliceID() SliceID { return SliceID{Platform: CorePlatform} }

// IsCore reports whether the slice ID is the platform-independent core slice.
func (id SliceID) IsCore() bool { return id.Platform == CorePlatform }

// String returns the slice ID in its canonical tag form, which is the form used
// as an index [slices] key and in a manifest platforms list.
func (id SliceID) String() string {
	if id.Architecture == "" {
		return id.Platform
	}
	return id.Platform + componentSeparator + id.Architecture
}

// ParseDeclaredPlatform parses one entry of a manifest platforms list. It
// rejects the literal "core", which is implicit in every needed slice set and
// therefore may not be declared.
func ParseDeclaredPlatform(tag string) (SliceID, error) {
	if tag == CorePlatform {
		return SliceID{}, manifestErrorf("platform %q is implicit and may not be declared; every project receives the core slice", CorePlatform)
	}
	return parsePlatformTag(tag)
}

// ParseSliceID parses a slice ID as published in an index [slices] key. Unlike
// ParseDeclaredPlatform it accepts "core", which is a mandatory published slice.
func ParseSliceID(id string) (SliceID, error) {
	if id == CorePlatform {
		return CoreSliceID(), nil
	}
	return parsePlatformTag(id)
}

// parsePlatformTag parses a platform tag of one or two components. Tags are
// matched exactly: a tag that is not lowercase is rejected rather than
// normalized, because Godot tags are lowercase and silently normalizing one
// hides a typo instead of reporting it.
func parsePlatformTag(tag string) (SliceID, error) {
	components, err := splitTag(tag)
	if err != nil {
		return SliceID{}, err
	}
	if len(components) > 2 {
		return SliceID{}, manifestErrorf(
			"invalid platform tag %q: a slice ID is a platform with an optional architecture, so it has at most two components",
			tag,
		)
	}
	platform := components[0]
	if !slices.Contains(knownPlatforms, platform) {
		return SliceID{}, unknownPlatformError(tag, platform)
	}
	parsed := SliceID{Platform: platform}
	if len(components) == 2 {
		architecture := components[1]
		if !slices.Contains(knownArchitectures, architecture) {
			return SliceID{}, manifestErrorf(
				"invalid platform tag %q: unknown architecture %q; known architectures are %s",
				tag, architecture, strings.Join(knownArchitectures, ", "),
			)
		}
		parsed.Architecture = architecture
	}
	return parsed, nil
}

// splitTag splits a tag on "." after rejecting the shapes that are never a tag:
// an empty tag, a tag carrying whitespace, and a tag with an empty component,
// which is what a leading, trailing, or doubled separator produces.
func splitTag(tag string) ([]string, error) {
	if tag == "" {
		return nil, manifestErrorf("invalid platform tag: tag is empty")
	}
	if strings.ContainsFunc(tag, unicode.IsSpace) {
		return nil, manifestErrorf("invalid platform tag %q: tags contain no whitespace", tag)
	}
	components := strings.Split(tag, componentSeparator)
	if slices.Contains(components, "") {
		return nil, manifestErrorf("invalid platform tag %q: tags have no empty component", tag)
	}
	return components, nil
}

func unknownPlatformError(tag, platform string) error {
	return manifestErrorf(
		"invalid platform tag %q: unknown platform %q; known platforms are %s",
		tag, platform, strings.Join(knownPlatforms, ", "),
	)
}

func fetchErrorf(format string, arguments ...any) error {
	return &output.FetchError{Err: fmt.Errorf(format, arguments...)}
}

func manifestErrorf(format string, arguments ...any) error {
	return &output.ManifestError{Err: fmt.Errorf(format, arguments...)}
}
