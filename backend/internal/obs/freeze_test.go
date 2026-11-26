package obs

import (
	"context"
	"reflect"
	"testing"
)

const metaFrozen = `<project name="p"><repository name="a" rebuild="local"/><repository name="b" rebuild="local"/></project>`
const metaPartial = `<project name="p"><repository name="a" rebuild="local"/><repository name="b"/></project>`
const metaUnfrozen = `<project name="p"><repository name="a"/><repository name="b" rebuild="transitive"/></project>`
const metaEmpty = `<project name="p"><title>t</title></project>`

func TestParseFreezeState(t *testing.T) {
	for meta, want := range map[string]string{metaFrozen: "frozen", metaPartial: "partial", metaUnfrozen: "unfrozen", metaEmpty: "empty"} {
		got, err := parseFreezeState([]byte(meta))
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", meta, got, err, want)
		}
	}
	if _, err := parseFreezeState([]byte("<not xml")); err == nil {
		t.Error("want an error for bad XML")
	}
}

// x: root frozen, containers frozen, tarballs absent -> frozen.
// y: root frozen, containers unfrozen -> partial.
func TestUnitFreeze_PerInstance(t *testing.T) {
	x := newFakeOBS(t, map[string]string{
		"/source/isv:percona:ppg:17/_meta":            metaFrozen,
		"/source/isv:percona:ppg:17:containers/_meta": metaFrozen,
	})
	y := newFakeOBS(t, map[string]string{
		"/source/percona:ppg:17/_meta":            metaFrozen,
		"/source/percona:ppg:17:containers/_meta": metaUnfrozen,
	})
	got := fleetOf(t, x, y).UnitFreeze(context.Background(), "ppg:17")
	want := []InstanceFreeze{{"x", "frozen"}, {"y", "partial"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// x hosts nothing of the unit (all 404); y: root unfrozen, tarballs empty.
func TestUnitFreeze_AbsentAndEmptyDoNotVote(t *testing.T) {
	x := newFakeOBS(t, map[string]string{})
	y := newFakeOBS(t, map[string]string{
		"/source/percona:ppg:17/_meta":          metaUnfrozen,
		"/source/percona:ppg:17:tarballs/_meta": metaEmpty,
	})
	got := fleetOf(t, x, y).UnitFreeze(context.Background(), "ppg:17")
	want := []InstanceFreeze{{"y", "unfrozen"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUnitFreeze_InstanceDown(t *testing.T) {
	x := newFakeOBS(t, map[string]string{"/source/isv:percona:ppg:17/_meta": metaFrozen})
	y := newFakeOBS(t, map[string]string{"/source/percona:ppg:17/_meta": metaFrozen})
	x.fail.Store(true)
	got := fleetOf(t, x, y).UnitFreeze(context.Background(), "ppg:17")
	want := []InstanceFreeze{{"x", "unknown"}, {"y", "frozen"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
