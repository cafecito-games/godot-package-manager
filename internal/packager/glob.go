package packager

import (
	"path"
	"strings"
)

// recursiveWildcard is the pattern segment that matches any number of path
// segments, including none.
const recursiveWildcard = "**"

// matchExtraPattern reports whether an extras glob matches one file path, both
// written with "/" separators and relative to addon_path.
//
// path.Match supplies the single-segment grammar — "*", "?", a character class,
// and an escape — and "**" is layered on top of it, because path.Match has no
// multi-segment wildcard and an .xcframework bundle cannot be named without one.
// A segment is matched against a segment, so "*" never crosses a separator,
// which is what makes "bin/*.so" mean the binaries directly in bin.
//
// A malformed segment never reaches here: validateExtraPattern rejects one while
// the config is loaded, so a pattern that path.Match would error on is reported
// as a config mistake rather than silently matching nothing.
func matchExtraPattern(pattern, filePath string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(filePath, "/"))
}

// matchSegments matches pattern segments against path segments, consuming "**"
// greedily by trying every number of path segments it could cover.
func matchSegments(patternSegments, pathSegments []string) bool {
	for len(patternSegments) > 0 {
		if patternSegments[0] == recursiveWildcard {
			rest := patternSegments[1:]
			// "**" matches zero or more segments, so every suffix of the
			// remaining path is a candidate. A trailing "**" matches whatever is
			// left, including nothing.
			for consumed := 0; consumed <= len(pathSegments); consumed++ {
				if matchSegments(rest, pathSegments[consumed:]) {
					return true
				}
			}
			return false
		}
		if len(pathSegments) == 0 {
			return false
		}
		matched, err := path.Match(patternSegments[0], pathSegments[0])
		if err != nil || !matched {
			return false
		}
		patternSegments = patternSegments[1:]
		pathSegments = pathSegments[1:]
	}
	return len(pathSegments) == 0
}
