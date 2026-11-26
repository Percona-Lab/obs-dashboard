package release

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

const (
	unitA = "ppg:staging:18"
	unitB = "ppg:staging:17:extras"
)

type fakeFreeze struct {
	mu    sync.Mutex
	state map[string][]InstanceFreeze
	calls map[string]int
}

func (f *fakeFreeze) UnitFreeze(_ context.Context, unit string) []InstanceFreeze {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[unit]++
	return f.state[unit]
}

func (f *fakeFreeze) callsOf(unit string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[unit]
}

func (f *fakeFreeze) set(unit string, inst ...InstanceFreeze) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == nil {
		f.state = map[string][]InstanceFreeze{}
	}
	f.state[unit] = inst
}

func freezeFixture(t *testing.T) (*svcFixture, *fakeFreeze) {
	t.Helper()
	fx := newFixture(t)
	src := &fakeFreeze{}
	fx.svc = NewService(Options{GitHub: fx.gh, DB: fx.db, Packages: fakeSource{}, Freeze: src, Now: fx.clock.now})
	return fx, src
}

func (fx *svcFixture) freezeRun(id int64, name, status, conclusion string, at time.Time) {
	fx.gh.runs = append(fx.gh.runs, Run{
		ID: id, Name: name, WorkflowFile: WorkflowFreeze, Status: status, Conclusion: conclusion,
		HTMLURL: "https://github.com/run/" + name, CreatedAt: at,
	})
}

func freezeOf(t *testing.T, s *Service, unit string) UnitFreeze {
	t.Helper()
	all, err := s.FreezeStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range all {
		if u.Unit == unit {
			return u
		}
	}
	t.Fatalf("no entry for %s in %+v", unit, all)
	return UnitFreeze{}
}

func TestFreezeStates_AllUnitsInFileOrder(t *testing.T) {
	fx, _ := freezeFixture(t)
	all, err := fx.svc.FreezeStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Unit != unitA || all[1].Unit != unitB {
		t.Fatalf("units = %+v", all)
	}
	for _, u := range all {
		if u.Instances == nil || u.State != "" {
			t.Errorf("%s: instances nil=%v state=%q", u.Unit, u.Instances == nil, u.State)
		}
	}
}

func TestFreezeStates_FrozenByFromAudit(t *testing.T) {
	fx, src := freezeFixture(t)
	src.set(unitA, InstanceFreeze{"labs", "frozen"})
	at := fx.clock.now()
	if err := fx.svc.SetFreeze(context.Background(), "maria@x", ActionFreeze, []string{unitA}); err != nil {
		t.Fatal(err)
	}
	fx.freezeRun(1, FreezeRunName(ActionFreeze, []string{unitA}), "completed", "success", at)
	got := freezeOf(t, fx.svc, unitA)
	if got.State != "frozen" || got.FrozenBy != "maria@x" || got.FrozenAt == nil || !got.FrozenAt.Equal(at) {
		t.Fatalf("got %+v", got)
	}
	if got.Pending != nil {
		t.Errorf("pending = %+v", got.Pending)
	}
}

func TestFreezeStates_FrozenByHiddenAfterUnfreeze(t *testing.T) {
	fx, src := freezeFixture(t)
	src.set(unitA, InstanceFreeze{"labs", "frozen"})
	ctx := context.Background()
	t0 := fx.clock.now()
	if err := fx.svc.SetFreeze(ctx, "maria@x", ActionFreeze, []string{unitA}); err != nil {
		t.Fatal(err)
	}
	fx.freezeRun(1, FreezeRunName(ActionFreeze, []string{unitA}), "completed", "success", t0)
	fx.clock.advance(10 * time.Minute)
	t1 := fx.clock.now()
	if err := fx.svc.SetFreeze(ctx, "rui@x", ActionUnfreeze, []string{unitA}); err != nil {
		t.Fatal(err)
	}
	fx.freezeRun(2, FreezeRunName(ActionUnfreeze, []string{unitA}), "completed", "success", t1)
	got := freezeOf(t, fx.svc, unitA)
	if got.State != "frozen" || got.FrozenBy != "" || got.FrozenAt != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestFreezeStates_UnknownInstanceIgnored(t *testing.T) {
	fx, src := freezeFixture(t)
	src.set(unitA, InstanceFreeze{"labs", "unknown"}, InstanceFreeze{"obs", "frozen"})
	got := freezeOf(t, fx.svc, unitA)
	if got.State != "frozen" || len(got.Instances) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestFreezeStates_PendingRun(t *testing.T) {
	fx, _ := freezeFixture(t)
	fx.gh.runs = append(fx.gh.runs, Run{
		ID: 7, Name: FreezeRunName(ActionFreeze, []string{unitA}), WorkflowFile: WorkflowFreeze,
		Status: "in_progress", HTMLURL: "https://github.com/run/7", CreatedAt: fx.clock.now(),
	})
	got := freezeOf(t, fx.svc, unitA)
	if got.Pending == nil || *got.Pending != (FreezeRun{Action: ActionFreeze, URL: "https://github.com/run/7", By: ""}) {
		t.Fatalf("pending = %+v", got.Pending)
	}
}

func TestFreezeStates_PendingByFromAudit(t *testing.T) {
	fx, _ := freezeFixture(t)
	at := fx.clock.now()
	if err := fx.svc.SetFreeze(context.Background(), "maria@x", ActionFreeze, []string{unitA}); err != nil {
		t.Fatal(err)
	}
	fx.gh.runs = append(fx.gh.runs, Run{
		ID: 7, Name: FreezeRunName(ActionFreeze, []string{unitA}), WorkflowFile: WorkflowFreeze,
		Status: "in_progress", HTMLURL: "https://github.com/run/7", CreatedAt: at.Add(3 * time.Second),
	})
	got := freezeOf(t, fx.svc, unitA)
	if got.Pending == nil || got.Pending.By != "maria@x" || got.Pending.URL != "https://github.com/run/7" {
		t.Fatalf("pending = %+v", got.Pending)
	}
}

func TestFreezeStates_PendingRememberedDispatch(t *testing.T) {
	fx, _ := freezeFixture(t)
	if err := fx.svc.SetFreeze(context.Background(), "maria@x", ActionFreeze, []string{unitA}); err != nil {
		t.Fatal(err)
	}
	got := freezeOf(t, fx.svc, unitA)
	if got.Pending == nil || *got.Pending != (FreezeRun{Action: ActionFreeze, URL: "", By: "maria@x"}) {
		t.Fatalf("pending = %+v", got.Pending)
	}
	if other := freezeOf(t, fx.svc, unitB); other.Pending != nil {
		t.Errorf("unit B pending = %+v", other.Pending)
	}
	fx.clock.advance(recentCutTTL)
	if got := freezeOf(t, fx.svc, unitA); got.Pending != nil {
		t.Errorf("pending after the TTL = %+v", got.Pending)
	}
}

func TestFreezeStates_RunWithoutAudit(t *testing.T) {
	fx, _ := freezeFixture(t)
	fx.freezeRun(1, FreezeRunName(ActionFreeze, []string{unitA, "not:a:unit"}), "in_progress", "", fx.clock.now())
	all, err := fx.svc.FreezeStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("units = %+v", all)
	}
	if all[0].Pending == nil || all[0].Pending.By != "" || all[0].Pending.Action != ActionFreeze {
		t.Errorf("pending = %+v", all[0].Pending)
	}
}

func failedFreezeFixture(t *testing.T) *svcFixture {
	fx, _ := freezeFixture(t)
	fx.freezeRun(5, FreezeRunName(ActionFreeze, []string{unitA, unitB}), "completed", "failure", fx.clock.now().Add(-time.Hour))
	fx.gh.jobs[5] = []Job{
		{ID: 1, Name: "freeze units (labs)", Status: "completed", Conclusion: "success"},
		{ID: 2, Name: "freeze units (obs)", Status: "completed", Conclusion: "failure"},
	}
	return fx
}

func TestFreezeStates_FailedRunNamesInstances(t *testing.T) {
	fx := failedFreezeFixture(t)
	for _, u := range []string{unitA, unitB} {
		got := freezeOf(t, fx.svc, u)
		want := FreezeFailure{Action: ActionFreeze, Instances: []string{"obs"}, URL: "https://github.com/run/" + FreezeRunName(ActionFreeze, []string{unitA, unitB})}
		if got.Failed == nil || got.Failed.Action != want.Action || got.Failed.URL != want.URL ||
			strings.Join(got.Failed.Instances, ",") != "obs" || got.Pending != nil {
			t.Errorf("%s: failed=%+v pending=%+v", u, got.Failed, got.Pending)
		}
	}
}

func TestFreezeStates_NewerRunClearsFailure(t *testing.T) {
	fx := failedFreezeFixture(t)
	fx.freezeRun(6, FreezeRunName(ActionFreeze, []string{unitA}), "completed", "success", fx.clock.now())
	if got := freezeOf(t, fx.svc, unitA); got.Failed != nil {
		t.Errorf("unit A failed = %+v", got.Failed)
	}
	if got := freezeOf(t, fx.svc, unitB); got.Failed == nil {
		t.Error("unit B should still be failed")
	}
}

func TestFreezeStates_MetaCached(t *testing.T) {
	fx, src := freezeFixture(t)
	src.set(unitA, InstanceFreeze{"labs", "frozen"})
	freezeOf(t, fx.svc, unitA)
	fx.clock.advance(30 * time.Second)
	freezeOf(t, fx.svc, unitA)
	if n := src.callsOf(unitA); n != 1 {
		t.Fatalf("reads within 30s = %d, want 1", n)
	}
	fx.clock.advance(31 * time.Second)
	freezeOf(t, fx.svc, unitA)
	if n := src.callsOf(unitA); n != 2 {
		t.Fatalf("reads after 61s = %d, want 2", n)
	}
}

func TestFreezeStates_RunFinishDropsCache(t *testing.T) {
	fx, src := freezeFixture(t)
	fx.freezeRun(1, FreezeRunName(ActionFreeze, []string{unitA}), "in_progress", "", fx.clock.now())
	freezeOf(t, fx.svc, unitA)
	if n := src.callsOf(unitA); n != 1 {
		t.Fatalf("reads = %d", n)
	}
	fx.clock.advance(5 * time.Second)
	fx.gh.runs[0].Status, fx.gh.runs[0].Conclusion = "completed", "success"
	freezeOf(t, fx.svc, unitA)
	if n := src.callsOf(unitA); n != 2 {
		t.Fatalf("reads after the run finished = %d, want 2", n)
	}
}

func TestSetFreeze_Dispatches(t *testing.T) {
	fx, _ := freezeFixture(t)
	if err := fx.svc.SetFreeze(context.Background(), "maria@x", ActionFreeze, []string{unitA, unitB}); err != nil {
		t.Fatal(err)
	}
	calls := fx.gh.callsOf("Dispatch")
	if len(calls) != 1 {
		t.Fatalf("dispatches = %v", calls)
	}
	for _, want := range []string{WorkflowFreeze, "action=freeze", "units=" + unitA + " " + unitB} {
		if !strings.Contains(calls[0], want) {
			t.Errorf("%q lacks %q", calls[0], want)
		}
	}
	rows, err := store.ReleaseAuditSince(fx.db, []string{auditFreeze}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Action != "freeze" || rows[0].Detail != unitA+" "+unitB ||
		rows[0].Branch != "" || rows[0].PR != 0 || rows[0].Email != "maria@x" {
		t.Fatalf("audit = %+v", rows)
	}
}

func TestSetFreeze_RefusesRightAfterDispatch(t *testing.T) {
	fx, _ := freezeFixture(t)
	ctx := context.Background()
	if err := fx.svc.SetFreeze(ctx, "maria@x", ActionFreeze, []string{unitA}); err != nil {
		t.Fatal(err)
	}
	err := fx.svc.SetFreeze(ctx, "maria@x", ActionUnfreeze, []string{unitA})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 1 {
		t.Fatalf("dispatches = %d", n)
	}
}

func TestSetFreeze_RefusesUnitWithRunInProgress(t *testing.T) {
	fx, _ := freezeFixture(t)
	fx.freezeRun(1, FreezeRunName(ActionFreeze, []string{unitA}), "in_progress", "", fx.clock.now())
	err := fx.svc.SetFreeze(context.Background(), "maria@x", ActionUnfreeze, []string{unitA})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 0 {
		t.Fatalf("dispatches = %d", n)
	}
}

func TestSetFreeze_BadInput(t *testing.T) {
	fx, _ := freezeFixture(t)
	cases := []struct {
		name, action string
		units        []string
	}{
		{"no units", ActionFreeze, nil},
		{"unknown action", "thaw", []string{unitA}},
		{"unknown unit", ActionFreeze, []string{"nope"}},
		{"duplicate", ActionFreeze, []string{unitA, unitA}},
	}
	for _, c := range cases {
		err := fx.svc.SetFreeze(context.Background(), "maria@x", c.action, c.units)
		if !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 0 {
		t.Fatalf("dispatches = %d", n)
	}
}

func TestSetFreeze_AlreadyFrozenAccepted(t *testing.T) {
	fx, src := freezeFixture(t)
	src.set(unitA, InstanceFreeze{"labs", "frozen"})
	if err := fx.svc.SetFreeze(context.Background(), "maria@x", ActionFreeze, []string{unitA}); err != nil {
		t.Fatal(err)
	}
	if n := len(fx.gh.callsOf("Dispatch")); n != 1 {
		t.Fatalf("dispatches = %d", n)
	}
}

func TestFreezeStates_NoSource(t *testing.T) {
	fx := newFixture(t)
	all, err := fx.svc.FreezeStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("units = %+v", all)
	}
	for _, u := range all {
		if u.State != "" || u.Instances == nil || len(u.Instances) != 0 {
			t.Errorf("%+v", u)
		}
	}
}

func TestSetFreeze_ConflictCarriesMessage(t *testing.T) {
	fx, _ := freezeFixture(t)
	fx.freezeRun(1, FreezeRunName(ActionFreeze, []string{unitA}), "in_progress", "", fx.clock.now())
	err := fx.svc.SetFreeze(context.Background(), "maria@x", ActionUnfreeze, []string{unitA})
	var ce *ConflictError
	if !errors.As(err, &ce) || !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if want := "unit " + unitA + " has a freeze in progress"; ce.Msg != want || err.Error() != want {
		t.Fatalf("msg = %q, error = %q, want %q", ce.Msg, err.Error(), want)
	}
}

func TestFreezeInstances_CancelledReadNotCached(t *testing.T) {
	fx, src := freezeFixture(t)
	src.set(unitA, InstanceFreeze{"labs", "frozen"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fx.svc.freezeInstances(ctx, []string{unitA})
	fx.svc.freezeInstances(context.Background(), []string{unitA})
	if n := src.callsOf(unitA); n != 2 {
		t.Fatalf("reads = %d, want 2 (the cancelled read must not be cached)", n)
	}
	fx.svc.freezeInstances(context.Background(), []string{unitA})
	if n := src.callsOf(unitA); n != 2 {
		t.Fatalf("reads = %d, want 2 (a good read is cached)", n)
	}
}

// slowFreeze blocks each read and records the peak number in flight.
type slowFreeze struct {
	mu        sync.Mutex
	cur, peak int
	calls     map[string]int
}

func (f *slowFreeze) UnitFreeze(_ context.Context, unit string) []InstanceFreeze {
	f.mu.Lock()
	f.cur++
	if f.cur > f.peak {
		f.peak = f.cur
	}
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[unit]++
	f.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	f.cur--
	f.mu.Unlock()
	return []InstanceFreeze{{"labs", "unfrozen"}}
}

func TestFreezeInstances_ConcurrencyCapped(t *testing.T) {
	fx := newFixture(t)
	src := &slowFreeze{}
	svc := NewService(Options{GitHub: fx.gh, DB: fx.db, Packages: fakeSource{}, Freeze: src, Now: fx.clock.now})
	var units []string
	for i := 0; i < 12; i++ {
		units = append(units, "u"+string(rune('a'+i)))
	}
	svc.freezeInstances(context.Background(), units)
	if src.peak > 4 || src.peak < 1 {
		t.Fatalf("peak concurrency = %d, want 1..4", src.peak)
	}
}

func TestFreezeInstances_ConcurrentCallersReadOnce(t *testing.T) {
	fx := newFixture(t)
	src := &slowFreeze{}
	svc := NewService(Options{GitHub: fx.gh, DB: fx.db, Packages: fakeSource{}, Freeze: src, Now: fx.clock.now})
	units := []string{"ua", "ub", "uc", "ud", "ue", "uf"}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.freezeInstances(context.Background(), units)
		}()
	}
	wg.Wait()
	for _, u := range units {
		if src.calls[u] != 1 {
			t.Fatalf("%s read %d times, want 1", u, src.calls[u])
		}
	}
}

// jobsFailGitHub fails Jobs only.
type jobsFailGitHub struct{ *fakeGitHub }

func (g jobsFailGitHub) Jobs(context.Context, int64) ([]Job, error) {
	return nil, errors.New("boom")
}

func TestFreezeStates_JobsErrorKeepsFailure(t *testing.T) {
	fx, src := freezeFixture(t)
	_ = src
	fx.freezeRun(5, FreezeRunName(ActionFreeze, []string{unitA}), "completed", "failure", fx.clock.now().Add(-time.Hour))
	fx.svc = NewService(Options{GitHub: jobsFailGitHub{fx.gh}, DB: fx.db, Packages: fakeSource{}, Freeze: &fakeFreeze{}, Now: fx.clock.now})
	got := freezeOf(t, fx.svc, unitA)
	if got.Failed == nil || got.Failed.Action != ActionFreeze || got.Failed.Instances == nil || len(got.Failed.Instances) != 0 {
		t.Fatalf("failed = %+v", got.Failed)
	}
}

// blockingFreeze blocks every read until release is closed.
type blockingFreeze struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingFreeze) UnitFreeze(context.Context, string) []InstanceFreeze {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-b.release
	return []InstanceFreeze{{"labs", "frozen"}}
}

// A caller that waits behind a hanging refresh stops waiting when its own
// context ends.
func TestFreezeInstances_WaiterLeavesWithItsContext(t *testing.T) {
	fx := newFixture(t)
	src := &blockingFreeze{started: make(chan struct{}, 1), release: make(chan struct{})}
	svc := NewService(Options{GitHub: fx.gh, DB: fx.db, Packages: fakeSource{}, Freeze: src, Now: fx.clock.now})
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.freezeInstances(context.Background(), []string{unitA})
	}()
	<-src.started

	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() {
		_, err := svc.freezeInstances(ctx, []string{unitA})
		res <- err
	}()
	cancel()
	select {
	case err := <-res:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter is still blocked behind the running refresh")
	}
	close(src.release)
	<-done
}

// SetFreeze decides on the runs only: it must not wait for OBS.
func TestSetFreeze_DoesNotReadOBS(t *testing.T) {
	fx, src := freezeFixture(t)
	src.set(unitA, InstanceFreeze{"labs", "unfrozen"})
	if err := fx.svc.SetFreeze(context.Background(), "maria@x", ActionFreeze, []string{unitA}); err != nil {
		t.Fatal(err)
	}
	if n := src.callsOf(unitA); n != 0 {
		t.Fatalf("OBS reads during SetFreeze = %d, want 0", n)
	}
}
