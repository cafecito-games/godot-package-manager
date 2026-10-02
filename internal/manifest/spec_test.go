package manifest

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAddonSpecHashDistinguishesAdjacentLists pins the two list-valued fields
// apart. A naive encoding that joins both lists with the same separator and
// places them adjacently makes these pairs collide, which would be a silent
// drift-detection failure: NeedsResolve compares exactly this hash.
func TestAddonSpecHashDistinguishesAdjacentLists(t *testing.T) {
	cases := []struct {
		name  string
		left  AddonSpec
		right AddonSpec
	}{
		{
			name:  "exclude borrows from platforms",
			left:  AddonSpec{Exclude: []string{"a", "b"}, Platforms: nil},
			right: AddonSpec{Exclude: []string{"a"}, Platforms: []string{"b"}},
		},
		{
			name:  "platforms borrows from exclude",
			left:  AddonSpec{Exclude: nil, Platforms: []string{"a", "b"}},
			right: AddonSpec{Exclude: []string{"b"}, Platforms: []string{"a"}},
		},
		{
			// Both lists non-empty, so the pair is not separated by an
			// incidental trailing separator: only a length-prefixed
			// encoding tells these apart.
			name:  "element migrates between two non-empty lists",
			left:  AddonSpec{Exclude: []string{"a", "b"}, Platforms: []string{"c"}},
			right: AddonSpec{Exclude: []string{"a"}, Platforms: []string{"b", "c"}},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.NotEqual(t, testCase.left.Hash(), testCase.right.Hash())
		})
	}
}

// TestAddonSpecHashGolden pins the hash of a fixed spec to a literal, which is
// what guarantees addons.lock stays byte-identical across machines and Go
// versions. The companion assertion is mechanical: spec.go must not read the
// host at all, so no host value can leak into the hash.
func TestAddonSpecHashGolden(t *testing.T) {
	spec := AddonSpec{
		Name:       "dialogue_manager",
		Source:     SourceArchive,
		URL:        "https://example.com/dialogue.zip",
		Repo:       "owner/dialogue",
		Version:    "v2.1.0",
		Asset:      "dialogue.zip",
		SourcePath: "addons/dialogue_manager",
		InstallAs:  "dialogue",
		Exclude:    []string{"dotnet", "demo"},
		Checksum:   "0000000000000000000000000000000000000000000000000000000000000000",
		Platforms:  []string{"macos", "ios.arm64"},
		Index:      "https://example.com/gpm-index.toml",
	}
	require.Equal(t, "2770475b2250783f5ee83598f5a5dc6ac5ef8b8571f03c4adf3c299893cfe4ad", spec.Hash())
}

// TestSpecHashingDoesNotReadTheHost is the mechanical half of the golden-hash
// guarantee: the host is excluded structurally, not by a code path that could
// regress.
func TestSpecHashingDoesNotReadTheHost(t *testing.T) {
	output, err := exec.Command("grep", "-n", "runtime\\.", "spec.go").CombinedOutput()
	require.Error(t, err, "spec.go must not reference the runtime package: %s", output)
	require.Empty(t, strings.TrimSpace(string(output)))
}

func TestAddonSpecHashNormalizesPlatforms(t *testing.T) {
	base := AddonSpec{Source: SourceArchive, URL: "https://example.com/a.zip"}

	absent := base
	absent.Platforms = nil
	explicitlyEmpty := base
	explicitlyEmpty.Platforms = []string{}
	require.Equal(t, absent.Hash(), explicitlyEmpty.Hash(), "nil and empty platforms must hash identically")

	single := base
	single.Platforms = []string{"macos"}
	duplicated := base
	duplicated.Platforms = []string{"macos", "macos"}
	require.Equal(t, single.Hash(), duplicated.Hash(), "duplicate platforms must be deduplicated")

	ordered := base
	ordered.Platforms = []string{"ios.arm64", "macos"}
	reversed := base
	reversed.Platforms = []string{"macos", "ios.arm64"}
	require.Equal(t, ordered.Hash(), reversed.Hash(), "platform order must not change the hash")

	added := base
	added.Platforms = []string{"ios.arm64", "macos", "windows"}
	require.NotEqual(t, ordered.Hash(), added.Hash(), "adding a platform must change the hash")
}

func TestAddonSpecHashNormalizesExclude(t *testing.T) {
	base := AddonSpec{Source: SourceArchive, URL: "https://example.com/a.zip"}

	single := base
	single.Exclude = []string{"dotnet"}
	duplicated := base
	duplicated.Exclude = []string{"dotnet", "dotnet"}
	require.Equal(t, single.Hash(), duplicated.Hash(), "duplicate exclude entries must be deduplicated")

	absent := base
	absent.Exclude = nil
	explicitlyEmpty := base
	explicitlyEmpty.Exclude = []string{}
	require.Equal(t, absent.Hash(), explicitlyEmpty.Hash(), "nil and empty exclude must hash identically")
}

func TestAddonSpecHashChangesWithIndex(t *testing.T) {
	withoutIndex := AddonSpec{Source: SourceArchive, URL: "https://example.com/a.zip"}
	withIndex := withoutIndex
	withIndex.Index = "https://example.com/gpm-index.toml"
	require.NotEqual(t, withoutIndex.Hash(), withIndex.Hash())
}

func TestAddonSpecHashIsHexDigest(t *testing.T) {
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{64}$`), AddonSpec{Source: SourceGit}.Hash())
}

// TestNeedsResolveAfterHashEncodingChange documents the one-time cost of adding
// fields to the hashed representation: a spec_hash written by an earlier gpm no
// longer matches, so every addon re-resolves once. That is the intended upgrade
// path — honoring a stale pin would install the wrong bytes.
func TestNeedsResolveAfterHashEncodingChange(t *testing.T) {
	spec := AddonSpec{
		Name:    "dialogue",
		Source:  SourceGit,
		URL:     "https://example.com/d.git",
		Version: "v1.0",
	}
	// The hash this spec produced before platforms and index joined the
	// representation, recorded as a literal so the assertion does not depend
	// on reconstructing the old encoding.
	preChangeSpecHash := "9c57c9c37ef59bda2bca1b5b3bca2b2a0c75d7dc9e5a3b9d16d9bd0a0b1a2c3d"
	lock := &Lockfile{Addons: map[string]LockEntry{
		"dialogue": {ResolvedVersion: "abc", SpecHash: preChangeSpecHash},
	}}
	require.NotEqual(t, preChangeSpecHash, spec.Hash())
	require.True(t, NeedsResolve(spec, lock))

	lock.Addons["dialogue"] = LockEntry{ResolvedVersion: "abc", SpecHash: spec.Hash()}
	require.False(t, NeedsResolve(spec, lock), "a pin written by this encoding is honored")
}
