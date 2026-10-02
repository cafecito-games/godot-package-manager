package packager

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatchExtraPattern(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		path    string
		matches bool
	}{
		{name: "exact path", pattern: "android/limboai.aar", path: "android/limboai.aar", matches: true},
		{name: "exact path mismatch", pattern: "android/limboai.aar", path: "android/other.aar", matches: false},
		{name: "star within one segment", pattern: "bin/*.so", path: "bin/library.so", matches: true},
		{name: "star does not cross a separator", pattern: "bin/*.so", path: "bin/nested/library.so", matches: false},
		{name: "double star spans segments", pattern: "ios/Limbo.xcframework/**", path: "ios/Limbo.xcframework/a/b/c.a", matches: true},
		{name: "double star spans one segment", pattern: "ios/Limbo.xcframework/**", path: "ios/Limbo.xcframework/Info.plist", matches: true},
		{name: "double star matches no segment", pattern: "ios/**/Info.plist", path: "ios/Info.plist", matches: true},
		{name: "trailing double star needs the prefix", pattern: "ios/Limbo.xcframework/**", path: "ios/Other.xcframework/Info.plist", matches: false},
		{name: "leading double star", pattern: "**/*.so", path: "bin/linux/library.so", matches: true},
		{name: "character class", pattern: "bin/library.[ab]", path: "bin/library.b", matches: true},
		{name: "pattern longer than the path", pattern: "bin/a/b", path: "bin/a", matches: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.matches, matchExtraPattern(testCase.pattern, testCase.path))
		})
	}
}
