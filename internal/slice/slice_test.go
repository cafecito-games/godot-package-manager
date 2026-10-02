package slice

import (
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestParseDeclaredPlatformAcceptsPlatformAndArchitecture(t *testing.T) {
	for _, tag := range []string{
		"linux", "macos", "windows", "android", "ios", "web",
		"linux.x86_64", "macos.universal", "macos.arm64", "windows.x86_32",
		"android.arm32", "ios.arm64", "web.wasm32", "linux.rv64",
	} {
		parsed, err := ParseDeclaredPlatform(tag)
		require.NoError(t, err, "tag %q", tag)
		require.Equal(t, tag, parsed.String(), "String must round-trip %q", tag)
	}
}

func TestParseSliceIDAcceptsCoreWhileParseDeclaredPlatformRejectsIt(t *testing.T) {
	parsed, err := ParseSliceID("core")
	require.NoError(t, err)
	require.Equal(t, CoreSliceID(), parsed)
	require.Equal(t, "core", parsed.String())
	require.True(t, parsed.IsCore())

	_, err = ParseDeclaredPlatform("core")
	require.Error(t, err)
	require.Contains(t, err.Error(), "implicit")
	var manifestError *output.ManifestError
	require.True(t, errors.As(err, &manifestError))
	require.Equal(t, output.ExitManifest, output.CodeFor(err))
}

func TestParseSliceIDRoundTripsEveryKnownCombination(t *testing.T) {
	for _, platform := range KnownPlatforms() {
		parsed, err := ParseSliceID(platform)
		require.NoError(t, err)
		require.Equal(t, platform, parsed.String())
		require.False(t, parsed.IsCore())

		for _, architecture := range KnownArchitectures() {
			tag := platform + "." + architecture
			parsed, err := ParseSliceID(tag)
			require.NoError(t, err)
			require.Equal(t, platform, parsed.Platform)
			require.Equal(t, architecture, parsed.Architecture)
			require.Equal(t, tag, parsed.String())
		}
	}
}

func TestKnownVocabulariesAreTheDesignsSets(t *testing.T) {
	require.Equal(t, []string{"android", "ios", "linux", "macos", "web", "windows"}, KnownPlatforms())
	require.Equal(t, []string{"arm32", "arm64", "rv64", "universal", "wasm32", "x86_32", "x86_64"}, KnownArchitectures())
	require.Equal(t, []string{"debug", "editor", "release", "template_debug", "template_release"}, KnownBuildTargets())
}

// parseFailClosedRows covers every fail-closed row of issue #18 that applies to
// the two parse entry points. Each row is rejected by both entry points, except
// the two rows that encode the one rule where they differ: the literal "core".
func TestParseEntryPointsAreFailClosed(t *testing.T) {
	rows := []struct {
		name            string
		tag             string
		expectedMessage []string
	}{
		{"unknown platform", "solaris.x86_64", []string{"solaris.x86_64", "linux", "macos", "windows", "android", "ios", "web"}},
		{"unknown platform alone", "solaris", []string{"solaris"}},
		{"known platform unknown architecture", "ios.sparc", []string{"ios.sparc", "sparc", "arm64"}},
		{"architecture in platform position", "x86_64", []string{"x86_64"}},
		{"build target as architecture", "macos.debug", []string{"macos.debug", "debug"}},
		{"more than two components", "ios.arm64.extra", []string{"ios.arm64.extra"}},
		{"four components", "ios.template_release.arm64.extra", []string{"ios.template_release.arm64.extra"}},
		{"empty string", "", nil},
		{"whitespace only", "   ", nil},
		{"tab only", "\t", nil},
		{"leading dot", ".ios", nil},
		{"trailing dot", "ios.", nil},
		{"dot only", ".", nil},
		{"empty middle component", "ios..arm64", nil},
		{"surrounding whitespace", " ios ", nil},
		{"internal whitespace", "ios. arm64", nil},
		{"mixed case platform", "iOS", []string{"iOS"}},
		{"mixed case tag", "iOS.ARM64", []string{"iOS.ARM64"}},
		{"upper case architecture", "ios.ARM64", []string{"ios.ARM64"}},
		{"mixed case core", "Core", []string{"Core"}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			for entryPoint, parse := range map[string]func(string) (SliceID, error){
				"ParseDeclaredPlatform": ParseDeclaredPlatform,
				"ParseSliceID":          ParseSliceID,
			} {
				parsed, err := parse(row.tag)
				require.Error(t, err, "%s must reject %q", entryPoint, row.tag)
				require.Equal(t, SliceID{}, parsed, "%s must not return a partial slice ID", entryPoint)

				var manifestError *output.ManifestError
				require.True(t, errors.As(err, &manifestError), "%s must wrap a ManifestError", entryPoint)
				require.Equal(t, output.ExitManifest, output.CodeFor(err))
				for _, fragment := range row.expectedMessage {
					require.Contains(t, err.Error(), fragment)
				}
			}
		})
	}
}

// TestReduceLibraryKeyMatchesTheDesignTable asserts every row of the slice
// identity table in docs/superpowers/specs/2026-10-02-addon-platform-slices-design.md.
func TestReduceLibraryKeyMatchesTheDesignTable(t *testing.T) {
	rows := []struct {
		key      string
		expected string
	}{
		{"macos.debug", "macos"},
		{"ios.template_release", "ios"},
		{"ios.template_release.arm64", "ios.arm64"},
		{"windows.debug.x86_64", "windows.x86_64"},
		{"android.template_debug.arm64", "android.arm64"},
		{"web.template_release.wasm32", "web.wasm32"},
		{"macos", "macos"},
		{"ios.arm64", "ios.arm64"},
		{"macos.editor", "macos"},
		{"macos.template_release.universal", "macos.universal"},
		{"linux.release.rv64", "linux.rv64"},
		{"windows.x86_32.editor", "windows.x86_32"},
	}

	for _, row := range rows {
		reduced, err := ReduceLibraryKey(row.key)
		require.NoError(t, err, "key %q", row.key)
		require.Equal(t, row.expected, reduced.String(), "key %q", row.key)
	}
}

// TestReduceLibraryKeyDropsEveryNonArchitectureAxis asserts the axes a key may
// carry besides the architecture, in the shapes real producers emit them:
// godot-cpp's SCons build names the float precision in every library key, and
// Godot spells the iOS simulator as a variant of the ios platform. A slice
// carries all of each axis for its platform, so every one of them reduces away.
func TestReduceLibraryKeyDropsEveryNonArchitectureAxis(t *testing.T) {
	rows := []struct {
		key      string
		expected string
	}{
		// The iOS simulator variant, as SwiftGodot and every SwiftGodot-based
		// addon ships it: the device and simulator libraries are one slice.
		{"ios.simulator.debug", "ios"},
		{"ios.simulator.release", "ios"},
		{"ios.debug", "ios"},
		// godot-cpp's four-component keys: platform, architecture, precision,
		// build target.
		{"ios.arm64.single.debug", "ios.arm64"},
		{"ios.arm64.double.release", "ios.arm64"},
		{"windows.x86_32.single.debug", "windows.x86_32"},
		{"windows.x86_64.double.release", "windows.x86_64"},
		{"android.arm64.double.debug", "android.arm64"},
		{"web.wasm32.single.release", "web.wasm32"},
		// Precision with no architecture, which is what macOS universal
		// binaries produce.
		{"macos.single.debug", "macos"},
		{"macos.double.release", "macos"},
		// Every axis at once, in the orders Godot accepts.
		{"ios.simulator.arm64.double.release", "ios.arm64"},
		{"ios.arm64.double.release.simulator", "ios.arm64"},
	}

	for _, row := range rows {
		reduced, err := ReduceLibraryKey(row.key)
		require.NoError(t, err, "key %q", row.key)
		require.Equal(t, row.expected, reduced.String(), "key %q", row.key)
		roundTripped, err := ParseSliceID(reduced.String())
		require.NoError(t, err, "reduction of %q must be a parseable slice ID", row.key)
		require.Equal(t, reduced, roundTripped)
	}
}

// TestReduceLibraryKeyAcceptsEveryKeyOfRealGDExtensionFiles feeds the library
// and dependency keys of checked-in .gdextension files, in the shape published
// GDExtension addons ship them, through reduction byte-for-byte.
func TestReduceLibraryKeyAcceptsEveryKeyOfRealGDExtensionFiles(t *testing.T) {
	fixtures := map[string]map[string]string{
		"testdata/limboai.gdextension": {
			"linux.debug.x86_64":    "linux.x86_64",
			"macos.debug":           "macos",
			"web.release.wasm32":    "web.wasm32",
			"android.release.arm32": "android.arm32",
		},
		"testdata/godot_jolt.gdextension": {
			"windows.editor.x86_64":            "windows.x86_64",
			"macos.template_release.universal": "macos.universal",
			"ios.template_debug.arm64":         "ios.arm64",
		},
	}

	for path, spotChecks := range fixtures {
		keys := platformTaggedKeysOfFixture(t, path)
		require.NotEmpty(t, keys, "fixture %s yielded no keys", path)
		for _, key := range keys {
			reduced, err := ReduceLibraryKey(key)
			require.NoError(t, err, "fixture %s key %q", path, key)
			require.False(t, reduced.IsCore(), "a library key never reduces to core")
			roundTripped, err := ParseSliceID(reduced.String())
			require.NoError(t, err, "reduction of %q must be a parseable slice ID", key)
			require.Equal(t, reduced, roundTripped)
		}
		for key, expected := range spotChecks {
			require.Contains(t, keys, key, "fixture %s must contain key %q", path, key)
			reduced, err := ReduceLibraryKey(key)
			require.NoError(t, err)
			require.Equal(t, expected, reduced.String())
		}
	}
}

// platformTaggedKeysOfFixture reads the keys of the platform-tagged sections of
// a .gdextension file. Reading .gdextension files is a sibling issue's job, so
// this stays a minimal test helper over the fixture's exact bytes.
func platformTaggedKeysOfFixture(t *testing.T, path string) []string {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)

	var keys []string
	platformTagged := false
	for _, line := range strings.Split(string(contents), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			platformTagged = trimmed == "[libraries]" || trimmed == "[dependencies]"
			continue
		}
		if !platformTagged || trimmed == "" || strings.HasPrefix(trimmed, ";") {
			continue
		}
		key, _, found := strings.Cut(trimmed, "=")
		require.True(t, found, "unparsed line %q in %s", trimmed, path)
		keys = append(keys, strings.TrimSpace(key))
	}
	return keys
}

func TestReduceLibraryKeyIsFailClosed(t *testing.T) {
	rows := []struct {
		name            string
		key             string
		expectedMessage []string
	}{
		{"unknown platform", "solaris.x86_64", []string{"solaris", "linux", "macos", "windows", "android", "ios", "web"}},
		{"unknown component after known platform", "ios.sparc", []string{"sparc", "arm64", "template_release"}},
		{"unknown third component", "windows.debug.sparc", []string{"sparc", "x86_64", "debug"}},
		{"two architectures", "ios.arm64.x86_64", []string{"ios.arm64.x86_64"}},
		{"two build targets", "macos.debug.template_release", []string{"macos.debug.template_release"}},
		{"two float precisions", "macos.single.double.debug", []string{"macos.single.double.debug", "two float precisions"}},
		{"two platform variants", "ios.simulator.simulator.release", []string{"ios.simulator.simulator.release", "two platform variants"}},
		{"unknown component beside a precision", "macos.single.sparc.debug", []string{"sparc", "double, single", "simulator"}},
		{"more than five components", "ios.simulator.arm64.double.release.extra", []string{"ios.simulator.arm64.double.release.extra", "at most 5 components"}},
		{"empty string", "", nil},
		{"whitespace only", "  ", nil},
		{"leading dot", ".macos.debug", nil},
		{"trailing dot", "macos.debug.", nil},
		{"empty middle component", "macos..debug", nil},
		{"mixed case", "iOS.ARM64", []string{"iOS.ARM64"}},
		{"mixed case build target", "macos.Debug", []string{"Debug"}},
		{"core is not a library key platform", "core", []string{"core"}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			reduced, err := ReduceLibraryKey(row.key)
			require.Error(t, err)
			require.Equal(t, SliceID{}, reduced)
			var manifestError *output.ManifestError
			require.True(t, errors.As(err, &manifestError))
			require.Equal(t, output.ExitManifest, output.CodeFor(err))
			for _, fragment := range row.expectedMessage {
				require.Contains(t, err.Error(), fragment)
			}
		})
	}
}

// TestHostCandidatesMatchesTheDesignTable asserts the host candidate chains of
// docs/superpowers/specs/2026-10-02-addon-platform-slices-design.md.
func TestHostCandidatesMatchesTheDesignTable(t *testing.T) {
	rows := []struct {
		operatingSystem string
		architecture    string
		expected        []string
	}{
		{"darwin", "arm64", []string{"macos.arm64", "macos.universal", "macos"}},
		{"darwin", "amd64", []string{"macos.x86_64", "macos.universal", "macos"}},
		{"linux", "amd64", []string{"linux.x86_64", "linux"}},
		{"linux", "arm64", []string{"linux.arm64", "linux"}},
		{"windows", "amd64", []string{"windows.x86_64", "windows"}},
		{"windows", "arm64", []string{"windows.arm64", "windows"}},
	}

	for _, row := range rows {
		host := Host{OperatingSystem: row.operatingSystem, Architecture: row.architecture}
		candidates := HostCandidates(host)
		require.Equal(t, row.expected, sliceIDStrings(candidates), "host %s/%s", row.operatingSystem, row.architecture)
		for _, candidate := range candidates {
			roundTripped, err := ParseSliceID(candidate.String())
			require.NoError(t, err, "every candidate must be a valid slice ID")
			require.Equal(t, candidate, roundTripped)
		}
	}
}

func TestHostCandidatesIsEmptyForAHostWithNoChain(t *testing.T) {
	for _, host := range []Host{
		{OperatingSystem: "plan9", Architecture: "amd64"},
		{OperatingSystem: "linux", Architecture: "mips64"},
		{OperatingSystem: "", Architecture: ""},
	} {
		require.Empty(t, HostCandidates(host), "host %+v", host)
	}
}

func TestHostCandidatesIsDeterministicAndDoesNotShareState(t *testing.T) {
	host := Host{OperatingSystem: "darwin", Architecture: "arm64"}
	first := HostCandidates(host)
	first[0] = SliceID{Platform: "web"}
	second := HostCandidates(host)
	require.Equal(t, []string{"macos.arm64", "macos.universal", "macos"}, sliceIDStrings(second))
}

func TestCurrentHostReadsTheInjectedRuntimeValues(t *testing.T) {
	require.Equal(t, runtime.GOOS, CurrentHost().OperatingSystem)
	require.Equal(t, runtime.GOARCH, CurrentHost().Architecture)

	originalOperatingSystem, originalArchitecture := currentOperatingSystem, currentArchitecture
	t.Cleanup(func() {
		currentOperatingSystem, currentArchitecture = originalOperatingSystem, originalArchitecture
	})
	currentOperatingSystem, currentArchitecture = "windows", "arm64"
	require.Equal(t, Host{OperatingSystem: "windows", Architecture: "arm64"}, CurrentHost())
	require.Equal(t, []string{"windows.arm64", "windows"}, sliceIDStrings(HostCandidates(CurrentHost())))
}

func sliceIDStrings(ids []SliceID) []string {
	if ids == nil {
		return nil
	}
	strung := make([]string, 0, len(ids))
	for _, id := range ids {
		strung = append(strung, id.String())
	}
	return strung
}

func publishedSlices(t *testing.T, ids ...string) []SliceID {
	t.Helper()
	published := make([]SliceID, 0, len(ids))
	for _, id := range ids {
		parsed, err := ParseSliceID(id)
		require.NoError(t, err)
		published = append(published, parsed)
	}
	return published
}

func TestSelectSlicesReturnsCorePlusDeclaredPlusTheFirstPublishedHostCandidate(t *testing.T) {
	published := publishedSlices(t, "core", "ios.arm64", "android.arm64", "macos", "windows.x86_64")
	host := Host{OperatingSystem: "darwin", Architecture: "arm64"}

	selection, err := SelectSlices([]string{"ios.arm64", "android.arm64"}, host, published, SelectDeclaredPlatforms)
	require.NoError(t, err)
	require.Equal(t, []string{"core", "android.arm64", "ios.arm64", "macos"}, sliceIDStrings(selection.Slices))
	require.True(t, selection.HostSupported)
	require.Equal(t, "macos", selection.HostSlice.String())
}

func TestSelectSlicesPrefersTheMostSpecificPublishedHostCandidate(t *testing.T) {
	host := Host{OperatingSystem: "darwin", Architecture: "arm64"}

	selection, err := SelectSlices(nil, host, publishedSlices(t, "core", "macos", "macos.universal", "macos.arm64"), SelectDeclaredPlatforms)
	require.NoError(t, err)
	require.Equal(t, "macos.arm64", selection.HostSlice.String())
	require.Equal(t, []string{"core", "macos.arm64"}, sliceIDStrings(selection.Slices))

	selection, err = SelectSlices(nil, host, publishedSlices(t, "core", "macos", "macos.universal"), SelectDeclaredPlatforms)
	require.NoError(t, err)
	require.Equal(t, "macos.universal", selection.HostSlice.String())
}

func TestSelectSlicesDeduplicatesDeclaredEntries(t *testing.T) {
	published := publishedSlices(t, "core", "ios.arm64", "linux.x86_64")
	host := Host{OperatingSystem: "linux", Architecture: "amd64"}

	selection, err := SelectSlices([]string{"ios.arm64", "ios.arm64", "linux.x86_64"}, host, published, SelectDeclaredPlatforms)
	require.NoError(t, err)
	require.Equal(t, []string{"core", "ios.arm64", "linux.x86_64"}, sliceIDStrings(selection.Slices))
}

func TestSelectSlicesWithAllPlatformsReturnsEveryPublishedSlice(t *testing.T) {
	published := publishedSlices(t, "windows.x86_64", "core", "ios.arm64", "macos", "android.arm64")
	host := Host{OperatingSystem: "darwin", Architecture: "arm64"}

	selection, err := SelectSlices([]string{"ios.arm64"}, host, published, SelectAllPublishedSlices)
	require.NoError(t, err)
	require.Equal(t, []string{"core", "android.arm64", "ios.arm64", "macos", "windows.x86_64"}, sliceIDStrings(selection.Slices))
	require.True(t, selection.HostSupported)
}

func TestSelectSlicesReportsAnUnsupportedHostWithoutFailing(t *testing.T) {
	published := publishedSlices(t, "core", "ios.arm64", "android.arm64")

	for _, host := range []Host{
		{OperatingSystem: "darwin", Architecture: "arm64"},
		{OperatingSystem: "plan9", Architecture: "amd64"},
	} {
		selection, err := SelectSlices([]string{"ios.arm64"}, host, published, SelectDeclaredPlatforms)
		require.NoError(t, err, "an unsupported host is a diagnostic, not an error")
		require.False(t, selection.HostSupported, "host %+v", host)
		require.Equal(t, SliceID{}, selection.HostSlice)
		require.Equal(t, []string{"core", "ios.arm64"}, sliceIDStrings(selection.Slices))
	}
}

func TestSelectSlicesIsDeterministicAndDoesNotMutateItsArguments(t *testing.T) {
	declared := []string{"windows.x86_64", "ios.arm64", "android.arm64"}
	published := publishedSlices(t, "windows.x86_64", "core", "ios.arm64", "android.arm64", "macos")
	host := Host{OperatingSystem: "darwin", Architecture: "arm64"}

	first, err := SelectSlices(declared, host, published, SelectDeclaredPlatforms)
	require.NoError(t, err)
	second, err := SelectSlices(declared, host, published, SelectDeclaredPlatforms)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, []string{"core", "android.arm64", "ios.arm64", "macos", "windows.x86_64"}, sliceIDStrings(first.Slices))

	require.Equal(t, []string{"windows.x86_64", "ios.arm64", "android.arm64"}, declared, "declared must not be reordered")
	require.Equal(t, []string{"windows.x86_64", "core", "ios.arm64", "android.arm64", "macos"}, sliceIDStrings(published), "published must not be reordered")

	first.Slices[0] = SliceID{Platform: "web"}
	third, err := SelectSlices(declared, host, published, SelectDeclaredPlatforms)
	require.NoError(t, err)
	require.Equal(t, second, third, "results must not share state")
}

func TestSelectSlicesIsFailClosed(t *testing.T) {
	host := Host{OperatingSystem: "linux", Architecture: "amd64"}
	rows := []struct {
		name             string
		declared         []string
		published        []string
		mode             SelectionMode
		expectedExitCode output.ExitCode
		expectedMessage  []string
	}{
		{
			name:             "declared platform absent from the published set",
			declared:         []string{"ios.arm64"},
			published:        []string{"core", "linux.x86_64", "macos"},
			expectedExitCode: output.ExitFetch,
			expectedMessage:  []string{"ios.arm64", "core", "linux.x86_64", "macos"},
		},
		{
			name:             "declared platform absent with all platforms requested",
			declared:         []string{"ios.arm64"},
			published:        []string{"core", "linux.x86_64"},
			mode:             SelectAllPublishedSlices,
			expectedExitCode: output.ExitFetch,
			expectedMessage:  []string{"ios.arm64"},
		},
		{
			name:             "published set missing core",
			declared:         []string{"linux.x86_64"},
			published:        []string{"linux.x86_64", "macos"},
			expectedExitCode: output.ExitFetch,
			expectedMessage:  []string{"core"},
		},
		{
			name:             "published set empty",
			declared:         nil,
			published:        nil,
			expectedExitCode: output.ExitFetch,
			expectedMessage:  []string{"core"},
		},
		{
			name:             "published set missing core with all platforms requested",
			declared:         nil,
			published:        []string{"linux.x86_64"},
			mode:             SelectAllPublishedSlices,
			expectedExitCode: output.ExitFetch,
			expectedMessage:  []string{"core"},
		},
		{
			name:             "declared entry is an unknown platform",
			declared:         []string{"solaris.x86_64"},
			published:        []string{"core", "linux.x86_64"},
			expectedExitCode: output.ExitManifest,
			expectedMessage:  []string{"solaris"},
		},
		{
			name:             "declared entry is malformed",
			declared:         []string{"ios.arm64.extra"},
			published:        []string{"core", "linux.x86_64"},
			expectedExitCode: output.ExitManifest,
			expectedMessage:  []string{"ios.arm64.extra"},
		},
		{
			name:             "declared entry is empty",
			declared:         []string{""},
			published:        []string{"core", "linux.x86_64"},
			expectedExitCode: output.ExitManifest,
			expectedMessage:  nil,
		},
		{
			name:             "declared entry is core",
			declared:         []string{"core"},
			published:        []string{"core", "linux.x86_64"},
			expectedExitCode: output.ExitManifest,
			expectedMessage:  []string{"implicit"},
		},
		{
			name:             "declared entry is a build target tag",
			declared:         []string{"macos.debug"},
			published:        []string{"core", "macos"},
			expectedExitCode: output.ExitManifest,
			expectedMessage:  []string{"macos.debug"},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			selection, err := SelectSlices(row.declared, host, publishedSlices(t, row.published...), row.mode)
			require.Error(t, err)
			require.Equal(t, Selection{}, selection, "a failed selection returns no partial result")
			require.Equal(t, row.expectedExitCode, output.CodeFor(err))
			switch row.expectedExitCode {
			case output.ExitManifest:
				var manifestError *output.ManifestError
				require.True(t, errors.As(err, &manifestError))
			case output.ExitFetch:
				var fetchError *output.FetchError
				require.True(t, errors.As(err, &fetchError))
			}
			for _, fragment := range row.expectedMessage {
				require.Contains(t, err.Error(), fragment)
			}
		})
	}
}

// TestVocabulariesAreClosed proves the three vocabularies this package owns are
// closed: every member is handled by every consumer, every non-member is
// rejected by every consumer, and the sets are pairwise disjoint so that
// classifying a .gdextension key component is never ambiguous.
func TestVocabulariesAreClosed(t *testing.T) {
	consumers := map[string]func(string) (SliceID, error){
		"ParseDeclaredPlatform": ParseDeclaredPlatform,
		"ParseSliceID":          ParseSliceID,
		"ReduceLibraryKey":      ReduceLibraryKey,
	}

	t.Run("sets are pairwise disjoint and exclude core", func(t *testing.T) {
		sets := map[string][]string{
			"platforms":     KnownPlatforms(),
			"architectures": KnownArchitectures(),
			"buildTargets":  KnownBuildTargets(),
		}
		for name, values := range sets {
			require.NotEmpty(t, values)
			require.True(t, slices.IsSorted(values), "%s must be declared sorted", name)
			require.NotContains(t, values, CorePlatform, "%s must not contain the core slice ID", name)
			for _, value := range values {
				require.Equal(t, strings.ToLower(value), value, "%s member %q must be lowercase", name, value)
			}
		}
		for _, platform := range sets["platforms"] {
			require.NotContains(t, sets["architectures"], platform)
			require.NotContains(t, sets["buildTargets"], platform)
		}
		for _, architecture := range sets["architectures"] {
			require.NotContains(t, sets["buildTargets"], architecture)
		}
	})

	t.Run("every platform is handled by every consumer", func(t *testing.T) {
		for _, platform := range KnownPlatforms() {
			for name, consume := range consumers {
				parsed, err := consume(platform)
				require.NoError(t, err, "%s must accept platform %q", name, platform)
				require.Equal(t, SliceID{Platform: platform}, parsed)
			}
			selection, err := SelectSlices(
				[]string{platform},
				Host{OperatingSystem: "plan9", Architecture: "amd64"},
				publishedSlices(t, "core", platform),
				SelectDeclaredPlatforms,
			)
			require.NoError(t, err, "SelectSlices must accept declared platform %q", platform)
			require.Equal(t, []string{CorePlatform, platform}, sliceIDStrings(selection.Slices))
		}
	})

	t.Run("every architecture is handled by every consumer", func(t *testing.T) {
		for _, architecture := range KnownArchitectures() {
			tag := "linux." + architecture
			for name, consume := range consumers {
				parsed, err := consume(tag)
				require.NoError(t, err, "%s must accept %q", name, tag)
				require.Equal(t, SliceID{Platform: "linux", Architecture: architecture}, parsed)
			}
		}
	})

	t.Run("every build target is dropped by reduction and rejected by both parsers", func(t *testing.T) {
		for _, buildTarget := range KnownBuildTargets() {
			reduced, err := ReduceLibraryKey("linux." + buildTarget)
			require.NoError(t, err, "reduction must accept build target %q", buildTarget)
			require.Equal(t, SliceID{Platform: "linux"}, reduced, "a build target never appears in a slice ID")

			reduced, err = ReduceLibraryKey("linux." + buildTarget + ".x86_64")
			require.NoError(t, err)
			require.Equal(t, SliceID{Platform: "linux", Architecture: "x86_64"}, reduced)

			for _, name := range []string{"ParseDeclaredPlatform", "ParseSliceID"} {
				_, err := consumers[name]("linux." + buildTarget)
				require.Error(t, err, "%s must reject the build target axis", name)
				require.Equal(t, output.ExitManifest, output.CodeFor(err))
			}
		}
	})

	t.Run("non-members are rejected by every consumer", func(t *testing.T) {
		for _, tag := range []string{"solaris", "solaris.x86_64", "linux.sparc", "linux.editor_debug", "linux.x64"} {
			for name, consume := range consumers {
				_, err := consume(tag)
				require.Error(t, err, "%s must reject %q", name, tag)
				require.Equal(t, output.ExitManifest, output.CodeFor(err))
			}
		}
	})
}
