package upgrade

import "testing"

func TestValidVersion(t *testing.T) {
	for s, want := range map[string]bool{
		"v0.1.0": true, "v10.20.30": true, "0.1.0": false, "v0.1": false,
		"v0.1.0-rc1": false, "dev": false, "": false, "v0.1.0 ": false,
		"v99999999999999999999.0.0": false,
	} {
		if got := ValidVersion(s); got != want {
			t.Errorf("ValidVersion(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.10.0", "v0.9.0", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v0.2.0", "dev", false},
		{"dev", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
