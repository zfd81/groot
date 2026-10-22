package memory

import (
	"strings"
	"testing"
)

func TestTruncateTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  hello  ", "hello"},
		{strings.Repeat("a", 100), strings.Repeat("a", 100)},
		{strings.Repeat("a", 101), strings.Repeat("a", 100)},
		{strings.Repeat("中", 100), strings.Repeat("中", 100)},
		{strings.Repeat("中", 101), strings.Repeat("中", 100)},
		{"", ""},
	}
	for _, c := range cases {
		if got := TruncateTitle(c.in); got != c.want {
			t.Errorf("TruncateTitle(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
