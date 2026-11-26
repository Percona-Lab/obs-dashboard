package release

import (
	"reflect"
	"testing"
)

func TestRunNames(t *testing.T) {
	if got := CutRunName("release/20261111T143210"); got != "Cut release/20261111T143210" {
		t.Errorf("CutRunName = %q", got)
	}
	if got := ReleaseRunName(41); got != "Release PR #41" {
		t.Errorf("ReleaseRunName = %q", got)
	}
}

func TestInstanceOf(t *testing.T) {
	cases := []struct {
		name, prefix, want string
		ok                 bool
	}{
		{"Populate pre-release (obs-prod)", JobPopulatePrefix, "obs-prod", true},
		{"Wait for download CDN (obs-prod)", JobCDNPrefix, "obs-prod", true},
		{"Populate pre-release (obs-prod", JobPopulatePrefix, "", false},
		{"Populate pre-release ()", JobPopulatePrefix, "", false},
		{"Release to OBS (obs-prod)", JobCopyPrefix, "obs-prod", true},
		{"Delete the pre-release (obs-prod)", JobDeletePrefix, "obs-prod", true},
		{"Release to OBS (obs-prod)", JobPopulatePrefix, "", false},
	}
	for _, c := range cases {
		got, ok := InstanceOf(c.name, c.prefix)
		if got != c.want || ok != c.ok {
			t.Errorf("InstanceOf(%q, %q) = (%q, %v), want (%q, %v)", c.name, c.prefix, got, ok, c.want, c.ok)
		}
	}
}

func TestInstanceOfUnexpandedMatrix(t *testing.T) {
	for _, c := range [][2]string{
		{"Populate pre-release (${{ matrix.instance.name }})", JobPopulatePrefix},
		{"Wait for download CDN (${{ matrix.instance.name }})", JobCDNPrefix},
	} {
		if got, ok := InstanceOf(c[0], c[1]); ok || got != "" {
			t.Errorf("InstanceOf(%q) = (%q, %v), want no instance", c[0], got, ok)
		}
	}
}

func TestParseFreezeRunName(t *testing.T) {
	a, u, ok := ParseFreezeRunName("freeze ppg:staging:18 ppg:staging:17:extras")
	if !ok || a != ActionFreeze || !reflect.DeepEqual(u, []string{"ppg:staging:18", "ppg:staging:17:extras"}) {
		t.Errorf("freeze: got (%q, %v, %v)", a, u, ok)
	}
	a, u, ok = ParseFreezeRunName(FreezeRunName(ActionUnfreeze, []string{"ppg:staging:16"}))
	if !ok || a != ActionUnfreeze || !reflect.DeepEqual(u, []string{"ppg:staging:16"}) {
		t.Errorf("round trip unfreeze: got (%q, %v, %v)", a, u, ok)
	}
	for _, bad := range []string{"", "freeze", "thaw ppg:staging:18", "Cut release/20261126T100000"} {
		if _, _, ok := ParseFreezeRunName(bad); ok {
			t.Errorf("ParseFreezeRunName(%q) ok, want false", bad)
		}
	}
}
