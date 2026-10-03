package packager

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

func sliceID(platform, architecture string) slice.SliceID {
	return slice.SliceID{Platform: platform, Architecture: architecture}
}

func TestFanOutPlanPublishesAGenericOnlyPlatformUnchanged(t *testing.T) {
	plan := newFanOutPlan([]slice.SliceID{sliceID("macos", ""), sliceID("linux", "x86_64")}, nil)

	require.False(t, plan.suppresses(sliceID("macos", "")))
	require.Equal(t, []slice.SliceID{sliceID("macos", "")}, plan.targetsOf(sliceID("macos", "")))
	require.Equal(t, []slice.SliceID{sliceID("linux", "x86_64")}, plan.targetsOf(sliceID("linux", "x86_64")))
}

func TestFanOutPlanSuppressesTheGenericSliceOfAMixedGranularityPlatform(t *testing.T) {
	plan := newFanOutPlan([]slice.SliceID{
		sliceID("macos", ""),
		sliceID("macos", "universal"),
		sliceID("macos", "arm64"),
		sliceID("windows", "x86_64"),
	}, nil)

	require.True(t, plan.suppresses(sliceID("macos", "")))
	require.Equal(t,
		[]slice.SliceID{sliceID("macos", "arm64"), sliceID("macos", "universal")},
		plan.targetsOf(sliceID("macos", "")),
	)
	require.Equal(t, []slice.SliceID{sliceID("macos", "universal")}, plan.targetsOf(sliceID("macos", "universal")))
	require.False(t, plan.suppresses(sliceID("windows", "x86_64")))
}

func TestFanOutPlanLeavesAnArchitectureOnlyPlatformAlone(t *testing.T) {
	plan := newFanOutPlan([]slice.SliceID{sliceID("ios", "arm64"), sliceID("ios", "arm32")}, nil)

	require.False(t, plan.suppresses(sliceID("ios", "arm64")))
	require.Equal(t, []slice.SliceID{sliceID("ios", "arm64")}, plan.targetsOf(sliceID("ios", "arm64")))
	require.Empty(t, plan.suppressedPlatforms())
}

func TestFanOutPlanReportsTheSuppressedPlatformsInOrder(t *testing.T) {
	plan := newFanOutPlan([]slice.SliceID{
		sliceID("macos", ""), sliceID("macos", "universal"),
		sliceID("ios", ""), sliceID("ios", "arm64"),
	}, nil)

	require.Equal(t, []slice.SliceID{sliceID("ios", ""), sliceID("macos", "")}, plan.suppressedPlatforms())
}

func TestFanOutMergesGenericEntriesIntoEveryArchitectureSlice(t *testing.T) {
	partitioned := map[slice.SliceID]map[string]slice.ExtensionEntries{
		sliceID("macos", ""): {
			"jolt.gdextension": {Libraries: slice.ExtensionEntryTable[string]{
				"macos.editor": "res://addons/jolt/bin/jolt_macos.framework",
			}},
		},
		sliceID("macos", "universal"): {
			"jolt.gdextension": {Libraries: slice.ExtensionEntryTable[string]{
				"macos.template_release.universal": "res://addons/jolt/bin/jolt_macos_universal.framework",
			}},
		},
	}
	plan := newFanOutPlan([]slice.SliceID{sliceID("macos", ""), sliceID("macos", "universal")}, nil)

	published, err := plan.applyTo(partitioned)
	require.NoError(t, err)
	require.NotContains(t, published, sliceID("macos", ""))
	require.Equal(t,
		slice.ExtensionEntryTable[string]{
			"macos.editor":                     "res://addons/jolt/bin/jolt_macos.framework",
			"macos.template_release.universal": "res://addons/jolt/bin/jolt_macos_universal.framework",
		},
		published[sliceID("macos", "universal")]["jolt.gdextension"].Libraries,
	)
}

func TestFanOutPlanWidensItsTargetsToADeclaredArchitecture(t *testing.T) {
	plan := newFanOutPlan(
		[]slice.SliceID{sliceID("macos", ""), sliceID("macos", "universal")},
		[]slice.SliceID{sliceID("macos", "arm64"), sliceID("android", "arm64")},
	)

	require.True(t, plan.suppresses(sliceID("macos", "")))
	require.Equal(t,
		[]slice.SliceID{sliceID("macos", "arm64"), sliceID("macos", "universal")},
		plan.targetsOf(sliceID("macos", "")),
	)
}

func TestFanOutPlanDoesNotFanOutAPlatformWhoseExtensionNamesNoArchitecture(t *testing.T) {
	plan := newFanOutPlan(
		[]slice.SliceID{sliceID("macos", "")},
		[]slice.SliceID{sliceID("macos", "arm64")},
	)

	require.False(t, plan.suppresses(sliceID("macos", "")))
	require.Equal(t, []slice.SliceID{sliceID("macos", "")}, plan.targetsOf(sliceID("macos", "")))
}

func TestFanOutPlanSuppressesAGenericSliceDeclaredOnlyByExtras(t *testing.T) {
	plan := newFanOutPlan(
		[]slice.SliceID{sliceID("android", "arm32"), sliceID("android", "arm64")},
		[]slice.SliceID{sliceID("android", "")},
	)

	require.True(t, plan.suppresses(sliceID("android", "")))
	require.Equal(t,
		[]slice.SliceID{sliceID("android", "arm32"), sliceID("android", "arm64")},
		plan.targetsOf(sliceID("android", "")),
	)
}

func TestFanOutPlanWidensADeclaredGenericSlicesTargetsToADeclaredArchitecture(t *testing.T) {
	plan := newFanOutPlan(
		[]slice.SliceID{sliceID("android", "arm64")},
		[]slice.SliceID{sliceID("android", ""), sliceID("android", "x86_64")},
	)

	require.Equal(t,
		[]slice.SliceID{sliceID("android", "arm64"), sliceID("android", "x86_64")},
		plan.targetsOf(sliceID("android", "")),
	)
}

func TestFanOutPlanDoesNotSuppressAGenericSliceDeclaredBesideNoArchitectureEntries(t *testing.T) {
	plan := newFanOutPlan(
		[]slice.SliceID{sliceID("linux", "x86_64")},
		[]slice.SliceID{sliceID("android", ""), sliceID("android", "arm64")},
	)

	require.False(t, plan.suppresses(sliceID("android", "")))
	require.Empty(t, plan.suppressedPlatforms())
}
