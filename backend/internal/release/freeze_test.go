package release

import (
	"testing"
	"time"
)

func TestFreezeRunsByUnit_NoPrefixMatch(t *testing.T) {
	runs := []Run{{ID: 1, Name: "freeze ppg:staging:17:extras", CreatedAt: mustTime()}}
	got := latestFreezeRuns(runs)
	if _, ok := got["ppg:staging:17:extras"]; !ok {
		t.Errorf("run not matched to its own unit: %v", got)
	}
	if _, ok := got["ppg:staging:17"]; ok {
		t.Errorf("run matched to a unit by prefix: %v", got)
	}
}

func TestLatestFreezeRuns_NewestWins(t *testing.T) {
	t0 := mustTime()
	runs := []Run{
		{ID: 1, Name: "freeze A B", CreatedAt: t0},
		{ID: 2, Name: "unfreeze B", CreatedAt: t0.Add(time.Hour)},
		{ID: 3, Name: "Cut release/20261126T100000", CreatedAt: t0.Add(2 * time.Hour)},
	}
	got := latestFreezeRuns(runs)
	if len(got) != 2 {
		t.Fatalf("got %d units, want 2: %v", len(got), got)
	}
	if got["A"].ID != 1 {
		t.Errorf("A: run %d, want 1", got["A"].ID)
	}
	if got["B"].ID != 2 {
		t.Errorf("B: run %d, want 2", got["B"].ID)
	}
}

func TestUnitFreezeState(t *testing.T) {
	inst := func(states ...string) []InstanceFreeze {
		out := make([]InstanceFreeze, 0, len(states))
		for i, s := range states {
			out = append(out, InstanceFreeze{Instance: string(rune('a' + i)), State: s})
		}
		return out
	}
	cases := []struct {
		name string
		in   []InstanceFreeze
		want string
	}{
		{"none", nil, ""},
		{"all frozen", inst("frozen", "frozen"), "frozen"},
		{"all unfrozen", inst("unfrozen"), "unfrozen"},
		{"mixed", inst("frozen", "unfrozen"), "partial"},
		{"partial", inst("partial"), "partial"},
		{"frozen ignores unknown", inst("frozen", "unknown"), "frozen"},
		{"only unknown", inst("unknown"), ""},
	}
	for _, c := range cases {
		if got := unitFreezeState(c.in); got != c.want {
			t.Errorf("%s: unitFreezeState = %q, want %q", c.name, got, c.want)
		}
	}
}
