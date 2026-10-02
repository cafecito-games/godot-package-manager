package slice

import (
	"errors"
	"os"
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
		{"more than three components", "windows.debug.x86_64.extra", []string{"windows.debug.x86_64.extra"}},
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
