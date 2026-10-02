package slice

import "slices"

// ExtensionSection names one platform-tagged section of a .gdextension file:
// a section whose keys are platform tags and whose values point at files that
// belong to one platform slice.
//
// This is the single declaration of that vocabulary. A package that partitions
// a .gdextension, writes an index, or reassembles an installed .gdextension
// iterates PartitionedSections rather than naming the sections itself, so
// adding a section in a future index format is one edit here.
type ExtensionSection string

const (
	// SectionLibraries is the .gdextension [libraries] section, which maps a
	// platform tag to the extension's library binary for that platform.
	SectionLibraries ExtensionSection = "libraries"

	// SectionDependencies is the .gdextension [dependencies] section, which maps
	// a platform tag to the files shipped beside that platform's library.
	SectionDependencies ExtensionSection = "dependencies"
)

// partitionedSections is the complete ordered set of partitioned sections for
// index format 1. The order is the order they are emitted and validated in, so
// that diagnostics over several sections are deterministic.
var partitionedSections = []ExtensionSection{SectionLibraries, SectionDependencies}

// PartitionedSections returns the .gdextension sections the index partitions per
// slice, in a fixed order.
func PartitionedSections() []ExtensionSection { return slices.Clone(partitionedSections) }
