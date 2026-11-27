package core

import "testing"

func TestNormalizeEOL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"a\nb\n", "a\nb\n"},
		{"a\r\nb\r\n", "a\nb\n"},
		{"a\r\nb\nc", "a\nb\nc"},
		{"\r\n", "\n"},
		{"", ""},
	}
	for _, c := range cases {
		if got := string(NormalizeEOL([]byte(c.in))); got != c.want {
			t.Errorf("NormalizeEOL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
