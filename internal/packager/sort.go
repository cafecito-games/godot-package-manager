package packager

import "sort"

// sortedKeys returns a map's keys in ascending order. Every map this package
// walks is visited through it, so diagnostics, archive membership, and the
// emitted index never depend on map iteration order.
func sortedKeys[Value any](table map[string]Value) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
