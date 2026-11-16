// Package units holds config value types shared by several packages.
package units

import (
	"fmt"
	"strconv"
	"strings"
)

// ByteSize is a size written as "2GB", "512MiB", "8 MiB" or a plain number
// of bytes. KB, MB, GB, TB are powers of 1000; KiB, MiB, GiB, TiB of 1024.
type ByteSize int64

var byteUnits = []struct {
	suffix string
	factor int64
}{
	{"TIB", 1 << 40}, {"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
	{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3},
	{"B", 1},
}

// UnmarshalText parses a size.
func (b *ByteSize) UnmarshalText(text []byte) error {
	s := strings.ToUpper(strings.TrimSpace(string(text)))
	factor := int64(1)
	for _, u := range byteUnits {
		if strings.HasSuffix(s, u.suffix) {
			factor = u.factor
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			break
		}
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 {
		return fmt.Errorf("%q is not a size like 2GB or 512MiB", string(text))
	}
	*b = ByteSize(n * float64(factor))
	return nil
}

// MarshalText writes the size in the largest unit that divides it
// exactly, decimal units first since that is how sizes are usually written.
func (b ByteSize) MarshalText() ([]byte, error) {
	for _, u := range append(byteUnits[4:8:8], byteUnits[:4]...) {
		if u.factor > 1 && int64(b) != 0 && int64(b)%u.factor == 0 {
			return []byte(strconv.FormatInt(int64(b)/u.factor, 10) + strings.Replace(u.suffix, "I", "i", 1)), nil
		}
	}
	return []byte(strconv.FormatInt(int64(b), 10)), nil
}

func (b ByteSize) String() string {
	t, _ := b.MarshalText()
	return string(t)
}
