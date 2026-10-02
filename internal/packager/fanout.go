package packager

import (
	"sort"

	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// fanOutPlan is the mixed-architecture-granularity decision for one addon: which
// generic platform slices are suppressed, and which architecture slices of their
// platform carry their entries instead.
//
// A Godot platform tag carries an architecture only sometimes, so one addon's
// .gdextension can split one platform's entries across the platform's generic
// slice and an architecture-specific slice of the same platform: godot_jolt
// writes macos.editor beside macos.template_release.universal. PartitionExtension
// reports each entry under exactly the slice its key reduces to, which is correct
// for one file but leaves a whole-addon question open, because the host candidate
// chain installs only the first published match it finds. A darwin/arm64 host
// offered both macos.universal and macos would install macos.universal alone and
// silently lose the editor library.
//
// The answer is producer-side and is this type: every architecture slice of such
// a platform receives that platform's generic entries in addition to its own, so
// each published slice is independently complete, and the platform's generic
// slice is then not published at all. Suppressing it is load-bearing rather than
// an optimization: publishing both would let one consumer select both — a
// project declaring macos whose host resolves to macos.universal, or any project
// using --all-platforms — and ReassembleExtension refuses two selected slices
// that declare the same entry key. Suppression leaves that fail-closed rule
// intact and keeps the host chain first-match.
//
// The index schema already anticipates this: an architecture-less entry key is
// accepted inside an architecture-specific slice, which is a permission with no
// other purpose.
//
// The plan is computed once per run, from the partitioned slice IDs alone, and is
// then the single authority for both archive membership and index entries, so
// the two cannot disagree about which slice carries what. It is a pure function
// of its input and its outputs are ordered, so two runs over an unchanged tree
// fan out identically.
type fanOutPlan struct {
	// replacements maps each suppressed generic slice ID to the architecture
	// slices of its platform, in ascending order.
	replacements map[slice.SliceID][]slice.SliceID
}

// newFanOutPlan derives the plan from the slice IDs an addon's .gdextension files
// partitioned into. The platform and architecture of each ID come from
// ReduceLibraryKey by way of PartitionExtension, so no platform tag is re-parsed
// here.
func newFanOutPlan(partitioned []slice.SliceID) fanOutPlan {
	architectures := map[string][]slice.SliceID{}
	generic := map[string]bool{}
	for _, id := range partitioned {
		if id.IsCore() {
			continue
		}
		if id.Architecture == "" {
			generic[id.Platform] = true
			continue
		}
		architectures[id.Platform] = append(architectures[id.Platform], id)
	}

	plan := fanOutPlan{replacements: map[slice.SliceID][]slice.SliceID{}}
	for platform := range generic {
		targets := architectures[platform]
		if len(targets) == 0 {
			// Case 1: the platform has generic entries only, so its generic slice
			// is published exactly as partitioned and no architecture slice is
			// invented for it.
			continue
		}
		sorted := append([]slice.SliceID(nil), targets...)
		sort.Slice(sorted, func(left, right int) bool { return sorted[left].String() < sorted[right].String() })
		plan.replacements[slice.SliceID{Platform: platform}] = sorted
	}
	return plan
}

// suppresses reports whether a slice ID is a generic slice the fan-out replaces,
// and which therefore is not published.
func (plan fanOutPlan) suppresses(id slice.SliceID) bool {
	_, replaced := plan.replacements[id]
	return replaced
}

// targetsOf returns the published slices that carry a partitioned slice's
// entries and files: the slice itself, unless it is a suppressed generic slice,
// in which case every architecture slice of its platform.
func (plan fanOutPlan) targetsOf(id slice.SliceID) []slice.SliceID {
	if targets, replaced := plan.replacements[id]; replaced {
		return targets
	}
	return []slice.SliceID{id}
}

// suppressedPlatforms returns the suppressed generic slice IDs in ascending
// order, for diagnostics that must name them deterministically.
func (plan fanOutPlan) suppressedPlatforms() []slice.SliceID {
	suppressed := make([]slice.SliceID, 0, len(plan.replacements))
	for id := range plan.replacements {
		suppressed = append(suppressed, id)
	}
	sort.Slice(suppressed, func(left, right int) bool {
		return suppressed[left].String() < suppressed[right].String()
	})
	return suppressed
}

// applyTo rewrites partitioned per-slice .gdextension entries into the entries
// of the published slices.
//
// A collision is impossible by construction — a generic key reduces to the
// generic slice and so is never also in an architecture slice — but it is
// checked rather than assumed, because a silent overwrite here would publish an
// index that disagrees with the archives.
func (plan fanOutPlan) applyTo(
	partitioned map[slice.SliceID]map[string]slice.ExtensionEntries,
) (map[slice.SliceID]map[string]slice.ExtensionEntries, error) {
	published := map[slice.SliceID]map[string]slice.ExtensionEntries{}
	for _, source := range sortedSliceIDs(partitioned) {
		for _, target := range plan.targetsOf(source) {
			if published[target] == nil {
				published[target] = map[string]slice.ExtensionEntries{}
			}
			for _, extensionPath := range sortedKeys(partitioned[source]) {
				merged, err := mergeExtensionEntries(
					published[target][extensionPath], partitioned[source][extensionPath], target, extensionPath,
				)
				if err != nil {
					return nil, err
				}
				published[target][extensionPath] = merged
			}
		}
	}
	return published, nil
}

// mergeExtensionEntries merges one .gdextension's entries into whatever the
// target slice already holds for that file, reporting a key declared twice.
func mergeExtensionEntries(
	into, from slice.ExtensionEntries,
	target slice.SliceID,
	extensionPath string,
) (slice.ExtensionEntries, error) {
	libraries, err := mergeEntryTable(into.Libraries, from.Libraries, target, extensionPath, slice.SectionLibraries)
	if err != nil {
		return slice.ExtensionEntries{}, err
	}
	dependencies, err := mergeEntryTable(into.Dependencies, from.Dependencies, target, extensionPath, slice.SectionDependencies)
	if err != nil {
		return slice.ExtensionEntries{}, err
	}
	return slice.ExtensionEntries{Libraries: libraries, Dependencies: dependencies}, nil
}

// mergeEntryTable merges one section's entries, keeping a nil table nil so the
// index writer still omits an absent section.
func mergeEntryTable[Value any](
	into, from slice.ExtensionEntryTable[Value],
	target slice.SliceID,
	extensionPath string,
	section slice.ExtensionSection,
) (slice.ExtensionEntryTable[Value], error) {
	if len(from) == 0 {
		return into, nil
	}
	merged := into
	if merged == nil {
		merged = slice.ExtensionEntryTable[Value]{}
	}
	for _, key := range sortedKeys(from) {
		if _, duplicate := merged[key]; duplicate {
			return nil, manifestErrorf(
				"slice %q would declare [%s] key %q of %q twice; a generic entry and an architecture-specific one cannot share a key",
				target, section, key, extensionPath,
			)
		}
		merged[key] = from[key]
	}
	return merged, nil
}

// sortedSliceIDs returns a slice-keyed map's keys in ascending tag order.
func sortedSliceIDs[Value any](table map[slice.SliceID]Value) []slice.SliceID {
	ids := make([]slice.SliceID, 0, len(table))
	for id := range table {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left].String() < ids[right].String() })
	return ids
}
