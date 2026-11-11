package release

import (
	"strings"
	"testing"

	"github.com/percona/obs-dashboard/internal/model"
)

type fakeSource map[string][]*model.Package

func (f fakeSource) Packages(prefix string) ([]*model.Package, error) {
	var out []*model.Package
	for proj, pk := range f {
		if proj == prefix || strings.HasPrefix(proj, prefix+":") {
			out = append(out, pk...)
		}
	}
	return out, nil
}

func pkg(project, name, version string, st model.RollupState, tags ...string) *model.Package {
	return &model.Package{Project: project, Name: name, Version: version, RollupState: st, ContainerTags: tags}
}

func testDef() UnitDef {
	return UnitDef{
		Unit:           "ppg-18",
		Members:        []string{"ppg:staging:18", "ppg:staging:18:containers", "ppg:staging:18:tarballs"},
		ReleaseProject: "ppg:releases:18",
		ReleaseDir:     "ppg/18",
	}
}

func TestParseUnitsRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "{}", `[{"unit":""}]`, `[]`, `[{"unit":"a","members":[]}]`} {
		if _, err := ParseUnits([]byte(in)); err == nil {
			t.Errorf("ParseUnits(%q) = nil error", in)
		}
	}
	defs, err := ParseUnits([]byte(`[{"unit":"a","members":["p:staging:1"],"release_project":"p:releases:1","release_dir":"p/1"}]`))
	if err != nil || len(defs) != 1 || defs[0].Unit != "a" {
		t.Fatalf("defs=%v err=%v", defs, err)
	}
}

func TestUnitTickable(t *testing.T) {
	src := fakeSource{
		"ppg:staging:18":            {pkg("ppg:staging:18", "pg", "18.2", model.RollupPublished)},
		"ppg:staging:18:containers": {pkg("ppg:staging:18:containers", "img", "18.2", model.RollupSucceeded, "18.2")},
		"ppg:releases:18":           {pkg("ppg:releases:18", "pg", "18.1", model.RollupPublished)},
	}
	rows, err := BuildUnitRows([]UnitDef{testDef()}, src, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if !r.Tickable || r.Blocked != nil || r.Built != r.Total || r.Total != 2 {
		t.Fatalf("row = %+v", r)
	}
}

func TestUnitNotBuilt(t *testing.T) {
	src := fakeSource{
		"ppg:staging:18":            {pkg("ppg:staging:18", "pg", "18.2", model.RollupPublished)},
		"ppg:staging:18:containers": {pkg("ppg:staging:18:containers", "img", "18.2", model.RollupFailed)},
	}
	rows, _ := BuildUnitRows([]UnitDef{testDef()}, src, nil, nil)
	r := rows[0]
	if r.Tickable || r.Blocked == nil || r.Blocked.Reason != "not_built" {
		t.Fatalf("row = %+v", r)
	}
	if len(r.Blocked.Packages) != 1 || r.Blocked.Packages[0] != "img" || r.Built != 1 || r.Total != 2 {
		t.Fatalf("row = %+v blocked=%+v", r, r.Blocked)
	}
	if r.Blocked.Text != "Not fully built: 1 package(s) not built." {
		t.Fatalf("text = %q", r.Blocked.Text)
	}
}

func TestUnitNotBuiltCapsNames(t *testing.T) {
	var pk []*model.Package
	for i := 0; i < 12; i++ {
		pk = append(pk, pkg("ppg:staging:18", string(rune('a'+i)), "1", model.RollupFailed))
	}
	rows, _ := BuildUnitRows([]UnitDef{testDef()}, fakeSource{"ppg:staging:18": pk}, nil, nil)
	if n := len(rows[0].Blocked.Packages); n != 10 {
		t.Fatalf("packages = %d", n)
	}
	if !strings.Contains(rows[0].Blocked.Text, "12 package(s)") {
		t.Fatalf("text = %q", rows[0].Blocked.Text)
	}
}

func TestUnitInFlightWins(t *testing.T) {
	src := fakeSource{"ppg:staging:18": {pkg("ppg:staging:18", "pg", "1", model.RollupFailed)}}
	rows, _ := BuildUnitRows([]UnitDef{testDef()}, src, map[string]string{"ppg-18": "r-7"}, nil)
	b := rows[0].Blocked
	if b == nil || b.Reason != "in_flight" || b.ReleaseID != "r-7" || b.Text != "Release r-7 is already in progress." || rows[0].Tickable {
		t.Fatalf("row = %+v blocked=%+v", rows[0], b)
	}
}

func TestUnitUnchanged(t *testing.T) {
	src := fakeSource{
		"ppg:staging:18":             {pkg("ppg:staging:18", "pg", "18.2", model.RollupPublished)},
		"ppg:staging:18:containers":  {pkg("ppg:staging:18:containers", "img", "18.2", model.RollupSucceeded, "b", "a")},
		"ppg:releases:18":            {pkg("ppg:releases:18", "pg", "18.2", model.RollupPublished)},
		"ppg:releases:18:containers": {pkg("ppg:releases:18:containers", "img", "18.2", model.RollupPublished, "a", "b")},
	}
	rows, _ := BuildUnitRows([]UnitDef{testDef()}, src, nil, map[string]string{"ppg-18": "r-3"})
	b := rows[0].Blocked
	if b == nil || b.Reason != "unchanged" || rows[0].Tickable {
		t.Fatalf("row = %+v", rows[0])
	}
	if b.Text != "Staging is identical to release r-3. Nothing changed since then." || rows[0].LastRelease != "r-3" {
		t.Fatalf("text = %q", b.Text)
	}
	rows, _ = BuildUnitRows([]UnitDef{testDef()}, src, nil, nil)
	if rows[0].Blocked.Text != "Staging is identical to the last release. Nothing changed since then." {
		t.Fatalf("text = %q", rows[0].Blocked.Text)
	}
}

func TestUnitNoReleaseYet(t *testing.T) {
	src := fakeSource{"ppg:staging:18": {pkg("ppg:staging:18", "pg", "18.2", model.RollupPublished)}}
	rows, _ := BuildUnitRows([]UnitDef{testDef()}, src, nil, nil)
	if !rows[0].Tickable || rows[0].Blocked != nil {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestUnitMembersAreExact(t *testing.T) {
	src := fakeSource{
		"ppg:staging:18":        {pkg("ppg:staging:18", "pg", "1", model.RollupPublished)},
		"ppg:staging:18:extras": {pkg("ppg:staging:18:extras", "x", "1", model.RollupFailed)},
	}
	rows, _ := BuildUnitRows([]UnitDef{testDef()}, src, nil, nil)
	if rows[0].Total != 1 || !rows[0].Tickable {
		t.Fatalf("row = %+v", rows[0])
	}
}
