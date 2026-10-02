//go:build windows

package packager

// openNoFollow has no Windows equivalent: the platform exposes no open flag that
// refuses a reparse point. The post-open regular-file check in writeArchiveEntry
// is the portable half of the same rule and applies on every platform.
const openNoFollow = 0
