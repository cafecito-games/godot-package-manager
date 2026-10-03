package packager

import (
	"fmt"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// fileClaim is one published slice's claim on one file of the addon subtree.
type fileClaim struct {
	id slice.SliceID

	// origin describes where the claim came from, so a conflict names both sides
	// in the author's own terms rather than only the slice IDs.
	origin string

	// fannedOut marks a claim the mixed-architecture-granularity fan-out created:
	// a file named by a generic entry of this slice's platform, carried by this
	// architecture slice because the platform's generic slice is suppressed.
	//
	// It is the one condition that lets two slices claim one file, so it is
	// recorded where the claim is made rather than re-derived later.
	fannedOut bool
}

// claimSet records which published slices claim which files.
type claimSet struct {
	byPath map[string][]fileClaim
}

func newClaimSet() *claimSet {
	return &claimSet{byPath: map[string][]fileClaim{}}
}

func (set *claimSet) add(relativePath string, claim fileClaim) {
	set.byPath[relativePath] = append(set.byPath[relativePath], claim)
}

// resolve assigns every file of the addon subtree to the slices that carry it.
//
// The partitions it returns are total and disjoint: every regular file under
// the addon root lands in exactly one slice or one shared artifact. A file named
// by a generic entry of a fanned-out platform is first proved to belong to all
// architecture targets, then stored once in that platform's artifact. Both
// halves are asserted here rather than assumed, because an archive set that
// drops a file or duplicates one across unrelated slices is a release an author
// would publish without noticing.
//
// A file no claim names belongs to core, which is what makes core "everything
// platform-independent" without enumerating it.
//
// A .gdextension is never claimed: it is partitioned, and core ships the body
// that is left. A claim on one means an extras glob or an entry value names a
// file that is also being rewritten, which is ambiguous rather than additive.
func (set *claimSet) resolve(tree *addonTree, coreID slice.SliceID) (map[slice.SliceID][]string, map[string][]string, error) {
	membership := map[slice.SliceID][]string{}
	artifacts := map[string][]string{}
	for _, file := range tree.files {
		claims := set.byPath[file.relativePath]
		if strings.HasSuffix(file.relativePath, extensionSuffix) {
			if len(claims) > 0 {
				return nil, nil, manifestErrorf(
					"%s is a %s, which is partitioned and always ships in the %q slice, but %s claims it for slice %q",
					file.relativePath, extensionSuffix, coreID, claims[0].origin, claims[0].id,
				)
			}
			membership[coreID] = append(membership[coreID], file.relativePath)
			continue
		}
		carriers, err := carriersOf(file.relativePath, claims)
		if err != nil {
			return nil, nil, err
		}
		if len(carriers) == 0 {
			membership[coreID] = append(membership[coreID], file.relativePath)
			continue
		}
		if len(carriers) > 1 {
			// carriersOf has proved this is exactly one generic platform's
			// fan-out. Format 2 stores those bytes once under that platform's
			// non-selectable artifact and makes every architecture slice depend
			// on it, instead of copying them into each slice archive.
			artifacts[carriers[0].Platform] = append(artifacts[carriers[0].Platform], file.relativePath)
			continue
		}
		for _, id := range carriers {
			membership[id] = append(membership[id], file.relativePath)
		}
	}
	// Every claimed path is a walked path by construction, because a claim is
	// only ever made for a path the tree walk produced. Asserted rather than
	// trusted: this is the invariant that keeps a file from outside the addon
	// root, or one reached through a symlink, out of an archive.
	for _, relativePath := range sortedKeys(set.byPath) {
		if _, walked := tree.positions[relativePath]; !walked {
			return nil, nil, manifestErrorf(
				"%s is claimed for slice %q but is not a file under the addon root",
				relativePath, set.byPath[relativePath][0].id,
			)
		}
	}
	return membership, artifacts, nil
}

// carriersOf reduces one file's claims to its distinct logical architecture
// owners, applying the fan-out's narrow multi-owner exemption. resolve stores a
// multi-owner file in their one shared artifact rather than in those slices.
func carriersOf(relativePath string, claims []fileClaim) ([]slice.SliceID, error) {
	distinct := map[slice.SliceID][]fileClaim{}
	for _, claim := range claims {
		distinct[claim.id] = append(distinct[claim.id], claim)
	}
	carriers := sortedSliceIDs(distinct)
	if len(carriers) <= 1 {
		return carriers, nil
	}
	// The only accepted duplication is the fan-out's own: one platform's generic
	// entry named this file, the platform's generic slice is suppressed, and so
	// every architecture slice of that platform carries it. Anything else — two
	// extras globs, or an extras glob and an entry for a different platform —
	// would put one file in two unrelated archives, which a consumer installing
	// both slices cannot merge.
	platform := carriers[0].Platform
	for _, id := range carriers {
		if id.Platform != platform || id.Architecture == "" || !anyFannedOut(distinct[id]) {
			return nil, duplicateClaimError(relativePath, distinct, carriers)
		}
	}
	return carriers, nil
}

func anyFannedOut(claims []fileClaim) bool {
	for _, claim := range claims {
		if claim.fannedOut {
			return true
		}
	}
	return false
}

// duplicateClaimError names the path and the first two slices that claim it,
// with the origin of each claim, in ascending slice order.
func duplicateClaimError(
	relativePath string,
	distinct map[slice.SliceID][]fileClaim,
	carriers []slice.SliceID,
) error {
	descriptions := make([]string, 0, 2)
	for _, id := range carriers[:2] {
		descriptions = append(descriptions, fmt.Sprintf("slice %q by %s", id, distinct[id][0].origin))
	}
	return manifestErrorf(
		"%s is claimed by two slices: %s; a file belongs to one slice, apart from a generic entry fanned out across one platform's architecture slices",
		relativePath, strings.Join(descriptions, " and "),
	)
}
