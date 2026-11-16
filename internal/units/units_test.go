package units

import "testing"

func TestByteSize(t *testing.T) {
	cases := map[string]int64{
		"2GB":     2e9,
		"512MiB":  512 << 20,
		"8 MiB":   8 << 20,
		"1024":    1024,
		"1.5KiB":  1536,
		"10b":     10,
		"100 kb":  100e3,
		"0":       0,
		" 3TiB  ": 3 << 40,
	}
	for in, want := range cases {
		var b ByteSize
		if err := b.UnmarshalText([]byte(in)); err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if int64(b) != want {
			t.Errorf("%q = %d, want %d", in, b, want)
		}
		text, _ := b.MarshalText()
		var back ByteSize
		if err := back.UnmarshalText(text); err != nil || back != b {
			t.Errorf("%q: round trip via %q = %d, %v", in, text, back, err)
		}
	}
	for _, bad := range []string{"", "GB", "-1", "two", "5 parsecs"} {
		var b ByteSize
		if err := b.UnmarshalText([]byte(bad)); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}
