package slice

import (
	"slices"
	"strings"
)

// maximumLibraryKeyComponents is the widest .gdextension library key Godot
// writes: a platform, and then one component for each axis a key may carry —
// architecture, float precision, build target, and platform variant — in any
// order.
const maximumLibraryKeyComponents = 5

// ReduceLibraryKey reduces a .gdextension library or dependency key to the slice
// ID that owns the file it points at. Every axis but the architecture is dropped
// because a slice carries all of that axis for its platform, so "macos.debug"
// and "macos.template_release" both belong to the "macos" slice, and so do
// "ios.release" and "ios.simulator.release" to "ios".
//
// A component that belongs to no known axis is rejected rather than dropped:
// silently ignoring it would put the file in the wrong slice and turn a
// packaging mistake into a Godot export failure.
func ReduceLibraryKey(key string) (SliceID, error) {
	components, err := splitTag(key)
	if err != nil {
		return SliceID{}, err
	}
	if len(components) > maximumLibraryKeyComponents {
		return SliceID{}, manifestErrorf(
			"invalid .gdextension key %q: a key is a platform with at most an architecture, a float precision, a build target and a platform variant, so it has at most %d components",
			key, maximumLibraryKeyComponents,
		)
	}
	platform := components[0]
	if !slices.Contains(knownPlatforms, platform) {
		return SliceID{}, unknownPlatformError(key, platform)
	}

	reduced := SliceID{Platform: platform}
	// Each axis may be named once. A key naming one twice is a producer mistake
	// and is reported rather than resolved to whichever component came last.
	droppedSeen := map[string]bool{}
	droppedAxes := []struct {
		axis   string
		values []string
	}{
		{"build targets", knownBuildTargets},
		{"float precisions", knownPrecisions},
		{"platform variants", knownPlatformVariants},
	}

	for _, component := range components[1:] {
		if slices.Contains(knownArchitectures, component) {
			if reduced.Architecture != "" {
				return SliceID{}, manifestErrorf(
					"invalid .gdextension key %q: it names two architectures, %q and %q",
					key, reduced.Architecture, component,
				)
			}
			reduced.Architecture = component
			continue
		}

		matchedAxis := ""
		for _, dropped := range droppedAxes {
			if slices.Contains(dropped.values, component) {
				matchedAxis = dropped.axis
				break
			}
		}
		if matchedAxis == "" {
			return SliceID{}, manifestErrorf(
				"invalid .gdextension key %q: component %q is none of a known architecture (%s), build target (%s), float precision (%s) or platform variant (%s)",
				key, component,
				strings.Join(knownArchitectures, ", "),
				strings.Join(knownBuildTargets, ", "),
				strings.Join(knownPrecisions, ", "),
				strings.Join(knownPlatformVariants, ", "),
			)
		}
		if droppedSeen[matchedAxis] {
			return SliceID{}, manifestErrorf("invalid .gdextension key %q: it names two %s", key, matchedAxis)
		}
		droppedSeen[matchedAxis] = true
	}
	return reduced, nil
}
