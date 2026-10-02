package slice

import (
	"slices"
	"strings"
)

// Selection is the outcome of computing a project's needed slice set.
type Selection struct {
	// Slices is the needed set, deduplicated and ordered deterministically with
	// core first and the rest sorted, so a caller may write it to a lockfile or
	// a state file without churn.
	Slices []SliceID

	// HostSlice is the published slice supplying the host's binaries, or the
	// zero value when HostSupported is false.
	HostSlice SliceID

	// HostSupported reports whether the published set supplies the host. A false
	// value is a diagnostic rather than a failure: the addon may simply not
	// support this host, which is the author's statement and not a project
	// misconfiguration.
	HostSupported bool
}

// SelectionMode chooses how wide a needed slice set SelectSlices computes. The
// zero value is the default a project gets with no flags: the platforms it
// declares plus the host's own slice.
//
// A mode changes which slices are materialized and nothing else. Declared
// platforms are validated in every mode, because a platform the addon does not
// publish is a manifest mistake however little of the addon this machine wants.
type SelectionMode int

const (
	// SelectDeclaredPlatforms selects core, every declared platform, and the
	// host's own slice. This is the shipping default.
	SelectDeclaredPlatforms SelectionMode = iota
	// SelectAllPublishedSlices selects every slice the addon publishes, for
	// projects that vendor addons/ into git.
	SelectAllPublishedSlices
	// SelectHostOnly selects core and the host's own slice, ignoring the
	// declared platforms. It is a development convenience for checkouts that
	// only ever run the host's editor; declared platforms are still validated.
	SelectHostOnly
)

// SelectSlices computes the slices a project needs: the mandatory core slice,
// the platforms it declares, and the host's own slice, which is implicit so that
// a manifest written on one machine does not break a teammate on another.
//
// mode widens or narrows that set: SelectAllPublishedSlices adds every published
// slice, and SelectHostOnly drops the declared platforms, leaving core plus the
// host. Declared platforms are validated in every mode.
func SelectSlices(declared []string, host Host, published []SliceID, mode SelectionMode) (Selection, error) {
	publishedSet := make(map[SliceID]struct{}, len(published))
	for _, id := range published {
		publishedSet[id] = struct{}{}
	}
	if _, found := publishedSet[CoreSliceID()]; !found {
		return Selection{}, fetchErrorf(
			"the addon publishes no %q slice, which is mandatory; published slices are %s",
			CorePlatform, describeSliceIDs(published),
		)
	}

	needed := map[SliceID]struct{}{CoreSliceID(): {}}
	for _, tag := range declared {
		id, err := ParseDeclaredPlatform(tag)
		if err != nil {
			return Selection{}, err
		}
		if _, found := publishedSet[id]; !found {
			return Selection{}, fetchErrorf(
				"the addon publishes no slice for declared platform %q; published slices are %s",
				id, describeSliceIDs(published),
			)
		}
		// Validated above in every mode; only materialized when the mode asks
		// for the declared platforms.
		if mode != SelectHostOnly {
			needed[id] = struct{}{}
		}
	}

	selection := Selection{}
	for _, candidate := range HostCandidates(host) {
		if _, found := publishedSet[candidate]; found {
			selection.HostSlice = candidate
			selection.HostSupported = true
			needed[candidate] = struct{}{}
			break
		}
	}

	if mode == SelectAllPublishedSlices {
		for id := range publishedSet {
			needed[id] = struct{}{}
		}
	}
	selection.Slices = sortedSliceIDs(needed)
	return selection, nil
}

// sortedSliceIDs orders a needed set deterministically: core first, because it is
// always present and always extracted first, then the rest by their tag.
func sortedSliceIDs(set map[SliceID]struct{}) []SliceID {
	ordered := make([]SliceID, 0, len(set))
	for id := range set {
		ordered = append(ordered, id)
	}
	slices.SortFunc(ordered, func(left, right SliceID) int {
		switch {
		case left.IsCore() && right.IsCore():
			return 0
		case left.IsCore():
			return -1
		case right.IsCore():
			return 1
		default:
			return strings.Compare(left.String(), right.String())
		}
	})
	return ordered
}

// describeSliceIDs renders a published set for an error message in the same
// deterministic order a selection uses, so two runs report one message.
func describeSliceIDs(ids []SliceID) string {
	if len(ids) == 0 {
		return "(none)"
	}
	set := make(map[SliceID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	rendered := make([]string, 0, len(set))
	for _, id := range sortedSliceIDs(set) {
		rendered = append(rendered, id.String())
	}
	return strings.Join(rendered, ", ")
}
