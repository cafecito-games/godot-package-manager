package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// bytesizeValue is a pflag.Value that parses human-readable size strings such
// as "512MB", "1GiB", or a raw byte count into an int64.
type bytesizeValue int64

// suffixes maps recognized unit suffixes (lower-cased) to multipliers. Both
// decimal (KB, MB, GB, TB) and binary (KiB, MiB, GiB, TiB) units are accepted;
// a bare "k", "m", "g", "t" is treated as the binary form.
var bytesizeSuffixes = []struct {
	suffix     string
	multiplier int64
}{
	{"tib", 1 << 40}, {"gib", 1 << 30}, {"mib", 1 << 20}, {"kib", 1 << 10},
	{"tb", 1000 * 1000 * 1000 * 1000}, {"gb", 1000 * 1000 * 1000},
	{"mb", 1000 * 1000}, {"kb", 1000},
	{"t", 1 << 40}, {"g", 1 << 30}, {"m", 1 << 20}, {"k", 1 << 10},
	{"b", 1}, {"", 1},
}

func parseBytesize(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("empty size value")
	}
	lower := strings.ToLower(trimmed)
	for _, entry := range bytesizeSuffixes {
		if entry.suffix == "" || strings.HasSuffix(lower, entry.suffix) {
			numberPart := strings.TrimSpace(lower[:len(lower)-len(entry.suffix)])
			if numberPart == "" {
				continue
			}
			value, err := strconv.ParseFloat(numberPart, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid size %q: %w", raw, err)
			}
			if value < 0 {
				return 0, fmt.Errorf("size %q must be non-negative", raw)
			}
			return int64(value * float64(entry.multiplier)), nil
		}
	}
	return 0, fmt.Errorf("invalid size %q", raw)
}

func (b *bytesizeValue) Set(raw string) error {
	value, err := parseBytesize(raw)
	if err != nil {
		return err
	}
	*b = bytesizeValue(value)
	return nil
}

func (b *bytesizeValue) Type() string { return "bytesize" }

func (b *bytesizeValue) String() string {
	if b == nil || *b == 0 {
		return "0"
	}
	return strconv.FormatInt(int64(*b), 10)
}
