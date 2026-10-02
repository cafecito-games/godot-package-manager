package slice

import (
	"slices"
	"strings"
)

// maximumLibraryKeyComponents is the widest .gdextension library key Godot
// writes: a platform, a build target, and an architecture, in either order for
// the latter two.
const maximumLibraryKeyComponents = 3

// ReduceLibraryKey reduces a .gdextension library or dependency key to the slice
// ID that owns the file it points at. The build-target axis is dropped because a
// slice carries every build target for its platform, so "macos.debug" and
// "macos.template_release" both belong to the "macos" slice.
//
// A component that is neither a known architecture nor a known build target is
// rejected rather than dropped: silently ignoring it would put the file in the
// wrong slice and turn a packaging mistake into a Godot export failure.
func ReduceLibraryKey(key string) (SliceID, error) {
	components, err := splitTag(key)
	if err != nil {
		return SliceID{}, err
	}
	if len(components) > maximumLibraryKeyComponents {
		return SliceID{}, manifestErrorf(
			"invalid .gdextension key %q: a key is a platform with at most a build target and an architecture, so it has at most %d components",
			key, maximumLibraryKeyComponents,
		)
	}
	platform := components[0]
	if !slices.Contains(knownPlatforms, platform) {
		return SliceID{}, unknownPlatformError(key, platform)
	}

	reduced := SliceID{Platform: platform}
	buildTargetSeen := false
	for _, component := range components[1:] {
		switch {
		case slices.Contains(knownArchitectures, component):
			if reduced.Architecture != "" {
				return SliceID{}, manifestErrorf(
					"invalid .gdextension key %q: it names two architectures, %q and %q",
					key, reduced.Architecture, component,
				)
			}
			reduced.Architecture = component
		case slices.Contains(knownBuildTargets, component):
			if buildTargetSeen {
				return SliceID{}, manifestErrorf("invalid .gdextension key %q: it names two build targets", key)
			}
			buildTargetSeen = true
		default:
			return SliceID{}, manifestErrorf(
				"invalid .gdextension key %q: component %q is neither a known architecture (%s) nor a known build target (%s)",
				key, component,
				strings.Join(knownArchitectures, ", "),
				strings.Join(knownBuildTargets, ", "),
			)
		}
	}
	return reduced, nil
}
