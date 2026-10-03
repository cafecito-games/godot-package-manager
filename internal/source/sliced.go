package source

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// SliceResult records one slice published by an addon's gpm-index.toml.
type SliceResult struct {
	ID       slice.SliceID // the slice's ID, e.g. core or ios.arm64
	Checksum string        // SHA-256 recorded in the index for that slice's archive
	Size     int64         // archive size in bytes recorded in the index
}

// sliceAssetResolver maps a slice archive's bare file name, as the index
// declares it, to the URL that archive is downloaded from, reporting false when
// the publisher offers no such file.
type sliceAssetResolver func(fileName string) (string, bool)

// slicedIndexURL is the single decision function for whether an addon is sliced
// and, when it is, where its index is fetched from. Both release-shaped fetchers
// ask it rather than each deciding for itself.
//
// An archive source is sliced exactly when the manifest sets `index`, which is
// the only way a bare URL can announce one. A github-release discovers its index
// from the release's asset list instead, so a release that publishes no
// slice.IndexFileName asset is an ordinary single-asset release and takes
// today's path unchanged. Every other source, git included, is never sliced:
// slices are a property of published release artifacts rather than of a source
// checkout.
func slicedIndexURL(spec manifest.AddonSpec, resolve sliceAssetResolver) (string, bool) {
	switch spec.Source {
	case manifest.SourceArchive:
		if spec.Index == "" {
			return "", false
		}
		return spec.Index, true
	case manifest.SourceGitHubRelease:
		return resolve(slice.IndexFileName)
	default:
		return "", false
	}
}

// slicedFetcher is the download half both release-shaped fetchers share: it
// loads the index, selects the slices the project needs, downloads and verifies
// only those, merges them into one staging directory, and reassembles every
// partitioned .gdextension so the installed file describes exactly what is on
// disk. What differs between the two fetchers is only where a file name
// resolves to and which headers a request carries.
type slicedFetcher struct {
	// client overrides the HTTP client; nil uses defaultHTTPClient.
	client *http.Client
	// maxBytes caps each downloaded archive on its own, so a sliced addon's
	// budget is per slice rather than for the set.
	maxBytes int64
	// maxExtracted caps the merged tree across every slice together.
	maxExtracted int64
	// header, when non-nil, is applied to the index and archive requests.
	header http.Header
	// resolve maps a slice archive's file name to its download URL.
	resolve sliceAssetResolver
	// stagingPattern is the os.MkdirTemp pattern for the merged tree, so a
	// leftover directory still names the fetcher that made it.
	stagingPattern string
	// diagnostics are the notes the fetcher has already accumulated before the
	// index was read, such as a manifest field the sliced path ignores.
	diagnostics []string
	// selectionMode is handed to slice.SelectSlices unchanged. It arrives from
	// the Runner the CLI configured, which is the only place a mode is decided.
	selectionMode slice.SelectionMode
}

// fetch performs the sliced download. On success the returned FetchResult.Dir is
// the merged tree and the caller owns removing it, exactly as for an unsliced
// fetch. On any failure nothing is left behind: the staging directory is removed
// and every downloaded archive is a temp file removed as its slice completes.
func (f *slicedFetcher) fetch(ctx context.Context, spec manifest.AddonSpec, indexURL string) (FetchResult, error) {
	if err := requireNoArchiveChecksum(spec); err != nil {
		return FetchResult{}, err
	}
	indexBytes, err := download(ctx, f.client, indexURL, f.header, f.maxBytes)
	if err != nil {
		return FetchResult{}, &output.FetchError{Err: fmt.Errorf(
			"downloading %s for addon %q: %s", slice.IndexFileName, spec.Name, err)}
	}
	// Computed over the bytes as served, before parsing, so the digest a lock
	// pins cannot be evaded by a change that only alters how the index parses.
	indexChecksum := slice.IndexChecksum(indexBytes)
	index, err := slice.LoadIndex(indexBytes)
	if err != nil {
		return FetchResult{}, err
	}

	published := index.PublishedSliceIDs()
	host := slice.CurrentHost()
	selection, err := slice.SelectSlices(spec.Platforms, host, published, f.selectionMode)
	if err != nil {
		return FetchResult{}, err
	}
	diagnostics := slices.Clone(f.diagnostics)
	if !selection.HostSupported {
		// Not a failure: the addon may simply not support this machine, which is
		// the publisher's statement rather than a project misconfiguration. The
		// declared slices plus core are still installed.
		diagnostics = append(diagnostics, fmt.Sprintf(
			"addon %q publishes no slice for this host (%s/%s); published slices are %s",
			spec.Name, host.OperatingSystem, host.Architecture, describeSliceIDs(published)))
	}

	staging, err := os.MkdirTemp("", f.stagingPattern)
	if err != nil {
		return FetchResult{}, &output.FetchError{Err: err}
	}
	if err := f.mergeSlices(ctx, index, selection.Slices, staging); err != nil {
		_ = os.RemoveAll(staging)
		return FetchResult{}, err
	}
	if err := reassembleExtensions(spec, index, selection.Slices, staging); err != nil {
		_ = os.RemoveAll(staging)
		return FetchResult{}, err
	}

	return FetchResult{
		Dir:             staging,
		ResolvedVersion: spec.Version,
		// Deliberately empty: a sliced addon has no single archive, and the
		// per-slice checksums verified above carry its integrity instead.
		Checksum:        "",
		IndexChecksum:   indexChecksum,
		PublishedSlices: publishedSliceResults(index, published),
		InstalledSlices: sortedSliceIDs(selection.Slices),
		Diagnostics:     diagnostics,
	}, nil
}

// requireNoArchiveChecksum rejects a manifest `checksum` on an addon that turns
// out to be sliced, before anything is downloaded.
//
// A sliced fetch reports no single archive checksum, because there is no single
// archive, so a declared one would be compared against nothing and silently pin
// nothing at all. An explicitly written verification directive going inert is
// worse than being told it does not apply here.
//
// It is an *output.ManifestError rather than an *output.FetchError: the remote
// content is fine and the user's declaration is what is wrong. The field is not
// reinterpreted as the index's checksum either — silently redefining what a
// field means is worse than rejecting it — and the index is already pinned, by
// the lockfile's index_sha256.
func requireNoArchiveChecksum(spec manifest.AddonSpec) error {
	if spec.Checksum == "" {
		return nil
	}
	return &output.ManifestError{Err: fmt.Errorf(
		"addon %q declares a checksum, but checksum pins one archive and a sliced addon publishes several, "+
			"so it has none to pin; the %s that names them is pinned by the lockfile's index_sha256 instead, "+
			"so remove checksum from this addon",
		spec.Name, slice.IndexFileName)}
}

// mergeSlices downloads, verifies, and extracts each needed slice into staging.
//
// One extract guard is built here and threaded through every extraction, so
// --max-extract-size bounds the merged tree rather than each archive. One owner
// map is likewise shared, so a path two slices both ship is reported with both
// slice IDs instead of resolving last-write-wins; because the check runs against
// the accumulated set rather than per archive, the slices may be extracted in
// any order and still produce the same tree or the same error.
func (f *slicedFetcher) mergeSlices(
	ctx context.Context,
	index *slice.Index,
	needed []slice.SliceID,
	staging string,
) error {
	guard := newMergeExtractGuard(f.maxExtracted)
	owners := map[string]extractedPath{}
	for _, id := range needed {
		indexSlice := index.Slices[id.String()]
		if indexSlice == nil {
			return fetchErrorf("the index publishes no slice %q", id)
		}
		if err := f.mergeSlice(ctx, id, indexSlice, staging, guard, owners); err != nil {
			return err
		}
	}
	return nil
}

// mergeSlice downloads one slice archive, verifies it against the index by both
// checksum and size, and extracts it into staging. A slice is never trusted
// because the index was: the index pins the bytes, and the bytes are checked
// against the pin.
func (f *slicedFetcher) mergeSlice(
	ctx context.Context,
	id slice.SliceID,
	indexSlice *slice.IndexSlice,
	staging string,
	guard *extractGuard,
	owners map[string]extractedPath,
) error {
	downloadURL, found := f.resolve(indexSlice.File)
	if !found {
		return fetchErrorf(
			"slice %q names archive %q, which the addon's publisher does not offer", id, indexSlice.File)
	}
	// The cap is applied per slice, which is what --max-download-size means: a
	// single archive the project is willing to download.
	archivePath, checksum, err := downloadToFile(ctx, f.client, downloadURL, f.header, f.maxBytes)
	if err != nil {
		return fetchErrorf("downloading slice %q: %s", id, err)
	}
	defer func() { _ = os.Remove(archivePath) }()

	if checksum != indexSlice.SHA256 {
		return fetchErrorf(
			"slice %q: checksum mismatch (index: %s, downloaded: %s)", id, indexSlice.SHA256, checksum)
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		return &output.FetchError{Err: err}
	}
	if info.Size() != indexSlice.Size {
		return fetchErrorf(
			"slice %q: size mismatch (index: %d bytes, downloaded: %d bytes)",
			id, indexSlice.Size, info.Size())
	}
	return extractArchiveInto(indexSlice.File, archivePath, staging, guard,
		func(relative string) (extractDisposition, error) {
			claimed := extractedPath{id: id, spelling: relative}
			key := mergeClaimKey(relative)
			owner, taken := owners[key]
			if !taken {
				owners[key] = claimed
				return extractDisposition{}, nil
			}
			// Two slices shipping one path is how `gpm package` fans a platform's
			// generic payload across the architecture slices of that platform, so
			// that each one installs on its own. A project selecting two of those
			// architectures — one declaring both Android ABIs, or any project run
			// with --all-platforms — therefore legitimately extracts the shared
			// file twice.
			//
			// Only that shape is accepted, which is exactly the duplication the
			// packager's own claim rule permits: two architecture slices of one
			// platform, under one spelling. Two unrelated slices sharing a path
			// stay a collision even when the bytes agree, because nothing about
			// such a release says the duplication was meant; so do two
			// differently-cased spellings, which are one file only on some hosts.
			if !sharedByFanOut(owner.id, id) || owner.spelling != relative {
				return extractDisposition{}, collisionError(owner, claimed)
			}
			return extractDisposition{
				shared:   true,
				mismatch: func() error { return divergentSharedPathError(owner, claimed) },
			}, nil
		})
}

// extractedPath records which slice shipped one path in the merged tree, and the
// spelling it shipped it under, so a collision can name both spellings when they
// differ.
type extractedPath struct {
	id       slice.SliceID
	spelling string
}

// mergeClaimKey reduces an extracted path to the identity two archives would
// collide on.
//
// The key is case-folded rather than the archive's exact spelling, because on a
// case-insensitive filesystem — the default on macOS and Windows — two
// differently-cased paths are one file, and an exact-spelling key would let the
// second slice silently truncate what the first wrote. Folding refuses the pair
// on every host instead of only on the hosts where it destroys data, which is
// also what keeps one index installing one tree everywhere.
func mergeClaimKey(relative string) string {
	return strings.ToLower(relative)
}

// collisionError reports two claims on one path. The claims are named in slice-ID
// order rather than extraction order, so the same pair reports one message
// however the slices were merged.
func collisionError(owner, claimed extractedPath) error {
	first, second := owner, claimed
	if second.id.String() < first.id.String() {
		first, second = second, first
	}
	const remedy = "one path in the merged tree cannot come from two slices"
	switch {
	case first.spelling == second.spelling:
		return fetchErrorf("slices %q and %q both ship %q; %s", first.id, second.id, first.spelling, remedy)
	case first.id == second.id:
		return fetchErrorf(
			"slice %q ships both %q and %q, which are one file on a case-insensitive filesystem; %s",
			first.id, first.spelling, second.spelling, remedy)
	default:
		return fetchErrorf(
			"slices %q and %q ship %q and %q, which are one file on a case-insensitive filesystem; %s",
			first.id, second.id, first.spelling, second.spelling, remedy)
	}
}

// sharedByFanOut reports whether two slices are ones `gpm package` fans a
// platform's generic payload across: distinct architecture slices of one
// platform.
//
// A generic slice is never published beside the architecture slices of its
// platform — the packager suppresses it precisely so a host's first match is a
// complete slice — so a fanned-out copy is only ever held by architecture
// slices, and this is the full set of pairs that may share a path.
func sharedByFanOut(owner, claimed slice.SliceID) bool {
	return owner != claimed &&
		owner.Platform == claimed.Platform &&
		owner.Architecture != "" && claimed.Architecture != ""
}

// divergentSharedPathError reports two slices shipping one path with different
// contents, which is the case the merge cannot resolve: either slice may be
// selected alone, so neither copy is the authoritative one, and installing
// whichever was extracted first would make the merged tree depend on slice
// order.
//
// It is named in slice-ID order for the same reason collisionError is: one pair
// reports one message however the slices were merged.
func divergentSharedPathError(owner, claimed extractedPath) error {
	first, second := owner, claimed
	if second.id.String() < first.id.String() {
		first, second = second, first
	}
	return fetchErrorf(
		"slices %q and %q both ship %q with different contents; a path shared by two slices must be the same file in both",
		first.id, second.id, first.spelling)
}

// reassembleExtensions rewrites every partitioned .gdextension in the merged
// tree, so the installed file lists exactly the entries of the slices that were
// installed and nothing else.
//
// A slice archive carries the addon subtree unprefixed, which is why the staging
// root is the addon root and an index's .gdextension path key locates the file
// directly under it.
func reassembleExtensions(
	spec manifest.AddonSpec,
	index *slice.Index,
	needed []slice.SliceID,
	staging string,
) error {
	// The index publishes its entry values against the addon's own directory,
	// which `gpm package` requires to be addons/<name>. A project may install the
	// addon somewhere else with install_as, so the two roots are derived
	// separately from one declaration and every entry is re-rooted between them.
	publishedRoot := slice.AddonResourceRoot(index.Name)
	installedRoot := slice.AddonResourceRoot(spec.InstallName())

	for _, extensionPath := range partitionedExtensionPaths(index, needed) {
		contributors, entries, err := extensionContributors(index, needed, extensionPath, publishedRoot, installedRoot)
		if err != nil {
			return err
		}
		destination, err := safeJoin(staging, filepath.FromSlash(extensionPath))
		if err != nil {
			return err
		}
		info, err := os.Stat(destination)
		if err != nil {
			return fetchErrorf(
				"the index declares entries for %q, but the %q slice ships no such file, so there is no body to reassemble them into",
				extensionPath, slice.CorePlatform)
		}
		core, err := os.ReadFile(destination)
		if err != nil {
			return &output.InstallError{Err: err}
		}
		reassembled, err := slice.ReassembleExtension(core, entries, contributors)
		if err != nil {
			return err
		}
		// Written after every slice is extracted and over the core body in place,
		// so the installed file describes exactly the tree beside it.
		if err := os.WriteFile(destination, reassembled, info.Mode().Perm()); err != nil {
			return &output.InstallError{Err: err}
		}
	}
	return nil
}

// extensionContributors collects, for one .gdextension file, the installed
// slices that declare entries for it and those entries re-rooted to the
// installed addon root.
//
// Only the contributing slices are reported, not every installed slice: an addon
// may ship several .gdextension files and a platform slice declares entries only
// for the ones whose binaries it carries, so requiring entries from every
// installed slice would reject a perfectly ordinary index.
func extensionContributors(
	index *slice.Index,
	needed []slice.SliceID,
	extensionPath string,
	publishedRoot, installedRoot string,
) ([]slice.SliceID, map[slice.SliceID]slice.ExtensionEntries, error) {
	contributors := []slice.SliceID{}
	entries := map[slice.SliceID]slice.ExtensionEntries{}
	for _, id := range needed {
		// The core slice carries no platform-tagged entry by definition, which is
		// what the index schema enforces too.
		if id.IsCore() {
			continue
		}
		indexSlice := index.Slices[id.String()]
		if indexSlice == nil {
			return nil, nil, fetchErrorf("the index publishes no slice %q", id)
		}
		declared := slice.ExtensionEntries{
			Libraries:    indexSlice.Libraries[extensionPath],
			Dependencies: indexSlice.Dependencies[extensionPath],
		}
		if declared.Libraries == nil && declared.Dependencies == nil {
			continue
		}
		rebased, err := slice.RebaseEntries(id, extensionPath, declared, publishedRoot, installedRoot)
		if err != nil {
			return nil, nil, err
		}
		contributors = append(contributors, id)
		entries[id] = rebased
	}
	return contributors, entries, nil
}

// partitionedExtensionPaths returns every .gdextension path the installed slices
// declare entries for, in ascending order, so a fetch reassembles the same files
// in the same order on every machine. The paths are read through the index's own
// structural accessor, walking the partitioned-section vocabulary rather than
// naming the sections here.
func partitionedExtensionPaths(index *slice.Index, needed []slice.SliceID) []string {
	set := map[string]struct{}{}
	for _, id := range needed {
		indexSlice := index.Slices[id.String()]
		if indexSlice == nil {
			continue
		}
		for _, section := range slice.PartitionedSections() {
			for _, extensionPath := range indexSlice.ExtensionPaths(section) {
				set[extensionPath] = struct{}{}
			}
		}
	}
	paths := make([]string, 0, len(set))
	for extensionPath := range set {
		paths = append(paths, extensionPath)
	}
	slices.Sort(paths)
	return paths
}

// publishedSliceResults reports every slice the index publishes, sorted by slice
// ID, including the ones this fetch did not download. The checksum of a slice
// that was downloaded has been verified against the bytes; the checksum of one
// that was not is a declaration carried out of the index, trustworthy exactly to
// the extent the index bytes are, which is what IndexChecksum pins.
func publishedSliceResults(index *slice.Index, published []slice.SliceID) []SliceResult {
	results := make([]SliceResult, 0, len(published))
	for _, id := range sortedSliceIDs(published) {
		indexSlice := index.Slices[id.String()]
		if indexSlice == nil {
			continue
		}
		results = append(results, SliceResult{ID: id, Checksum: indexSlice.SHA256, Size: indexSlice.Size})
	}
	return results
}

// sortedSliceIDs returns a copy of ids ordered by their canonical tag. A
// selection is ordered core-first because that is the order it is extracted in;
// a reported set is ordered by tag so it is stable to read and to write.
func sortedSliceIDs(ids []slice.SliceID) []slice.SliceID {
	if len(ids) == 0 {
		return nil
	}
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, func(left, right slice.SliceID) int {
		return strings.Compare(left.String(), right.String())
	})
	return sorted
}

// describeSliceIDs renders a published set for a diagnostic.
func describeSliceIDs(ids []slice.SliceID) string {
	if len(ids) == 0 {
		return "(none)"
	}
	rendered := make([]string, 0, len(ids))
	for _, id := range sortedSliceIDs(ids) {
		rendered = append(rendered, id.String())
	}
	return strings.Join(rendered, ", ")
}

// releaseSliceResolver resolves a slice archive's file name against a GitHub
// release's asset list. The asset's API URL is used rather than its browser
// download URL for the same reason the unsliced path uses it: it is the only one
// that works for a private repository.
func releaseSliceResolver(assets []ghAsset, rewrite func(string) string) sliceAssetResolver {
	byName := make(map[string]string, len(assets))
	for _, asset := range assets {
		byName[asset.Name] = asset.APIURL
	}
	return func(fileName string) (string, bool) {
		assetURL, found := byName[fileName]
		if !found {
			return "", false
		}
		if rewrite != nil {
			assetURL = rewrite(assetURL)
		}
		return assetURL, true
	}
}

// archiveSliceResolver resolves a slice archive's file name against the
// directory of the addon's index URL. An archive source publishes no asset list,
// so the index's own location is what says where the archives it describes sit,
// which is also what lets one index describe the set hosted beside it.
//
// The file name is a bare asset name the index schema has already validated to
// carry no separator and no scheme, so it can only name a sibling of the index.
func archiveSliceResolver(indexURL string) sliceAssetResolver {
	return func(fileName string) (string, bool) {
		parsed, err := url.Parse(indexURL)
		if err != nil || parsed.Path == "" {
			return "", false
		}
		resolved := *parsed
		resolved.RawQuery = ""
		resolved.Fragment = ""
		resolved.RawFragment = ""
		resolved.Path = path.Join(path.Dir(parsed.Path), fileName)
		// Cleared so the URL is re-escaped from the new path rather than from the
		// index URL's own encoding.
		resolved.RawPath = ""
		return resolved.String(), true
	}
}

func fetchErrorf(format string, arguments ...any) error {
	return &output.FetchError{Err: fmt.Errorf(format, arguments...)}
}
