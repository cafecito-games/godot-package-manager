package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseBytesize(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"0", 0},
		{"512", 512},
		{"512B", 512},
		{"1k", 1 << 10},
		{"1K", 1 << 10},
		{"2KiB", 2 << 10},
		{"1KB", 1000},
		{"4MiB", 4 << 20},
		{"1MB", 1000 * 1000},
		{"1GiB", 1 << 30},
		{"1GB", 1000 * 1000 * 1000},
		{"  64MiB  ", 64 << 20},
		{"0.5GiB", 1 << 29},
	}
	for _, testCase := range cases {
		got, err := parseBytesize(testCase.input)
		require.NoErrorf(t, err, "parseBytesize(%q)", testCase.input)
		require.Equalf(t, testCase.want, got, "parseBytesize(%q)", testCase.input)
	}
}

func TestParseBytesizeRejectsInvalid(t *testing.T) {
	for _, input := range []string{"", "   ", "abc", "12xb", "-1", "-5MiB", "MiB"} {
		_, err := parseBytesize(input)
		require.Errorf(t, err, "parseBytesize(%q) should fail", input)
	}
}

func TestBytesizeValueSetAndString(t *testing.T) {
	var value bytesizeValue
	require.NoError(t, value.Set("8KiB"))
	require.Equal(t, int64(8<<10), int64(value))
	require.Equal(t, "8192", value.String())

	var zero bytesizeValue
	require.Equal(t, "0", zero.String())
	require.Equal(t, "bytesize", zero.Type())

	require.Error(t, value.Set("nope"))
}
