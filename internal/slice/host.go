package slice

import (
	"runtime"
	"slices"
)

// currentOperatingSystem and currentArchitecture are what CurrentHost reports
// by default: the machine this process is actually running on.
var (
	currentOperatingSystem = runtime.GOOS
	currentArchitecture    = runtime.GOARCH
)

// Host is the machine gpm is running on, named in Go's runtime vocabulary
// rather than Godot's: the Godot platform tags a host maps to are the candidate
// chain, not the host itself.
type Host struct {
	OperatingSystem string
	Architecture    string
}

// CurrentHost returns the machine gpm is running on.
//
// It is a package-level variable rather than a plain function because it is the
// one seam the whole tree resolves a host through: the consumer that reconciles
// a needed slice set against this disk and the fetcher that materializes it both
// ask, and a test that simulated the host for only one of them would be
// asserting against a machine that cannot exist. No other package declares a
// host resolver of its own.
var CurrentHost = func() Host {
	return Host{OperatingSystem: currentOperatingSystem, Architecture: currentArchitecture}
}

// hostCandidateChains maps a host to the slice IDs that can supply its editor
// binaries, most specific first. A chain exists because Godot tags carry an
// architecture only sometimes and macOS libraries are usually universal, so the
// first chain entry an addon publishes is the one to install.
//
// This is the only declaration of the mapping. A host absent from it has no
// chain, which SelectSlices reports as an unsupported host rather than an error.
var hostCandidateChains = map[Host][]SliceID{
	{OperatingSystem: "darwin", Architecture: "arm64"}: {
		{Platform: "macos", Architecture: "arm64"},
		{Platform: "macos", Architecture: "universal"},
		{Platform: "macos"},
	},
	{OperatingSystem: "darwin", Architecture: "amd64"}: {
		{Platform: "macos", Architecture: "x86_64"},
		{Platform: "macos", Architecture: "universal"},
		{Platform: "macos"},
	},
	{OperatingSystem: "linux", Architecture: "amd64"}: {
		{Platform: "linux", Architecture: "x86_64"},
		{Platform: "linux"},
	},
	{OperatingSystem: "linux", Architecture: "arm64"}: {
		{Platform: "linux", Architecture: "arm64"},
		{Platform: "linux"},
	},
	{OperatingSystem: "windows", Architecture: "amd64"}: {
		{Platform: "windows", Architecture: "x86_64"},
		{Platform: "windows"},
	},
	{OperatingSystem: "windows", Architecture: "arm64"}: {
		{Platform: "windows", Architecture: "arm64"},
		{Platform: "windows"},
	},
}

// HostCandidates returns the slice IDs that can supply the host's binaries, most
// specific first, or nil for a host gpm has no chain for. The chain is fixed per
// host, so repeated runs on one machine select the same slice.
func HostCandidates(host Host) []SliceID {
	return slices.Clone(hostCandidateChains[host])
}
