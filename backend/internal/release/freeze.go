package release

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

const (
	auditFreeze   = ActionFreeze
	auditUnfreeze = ActionUnfreeze
	// freezeMetaTTL is how long the OBS freeze state of a unit is reused.
	freezeMetaTTL = time.Minute
	// freezeReadConcurrency is how many units are read from OBS at once.
	freezeReadConcurrency = 4
)

// freezeMetaEntry is one OBS read of a unit.
type freezeMetaEntry struct {
	at   time.Time
	inst []InstanceFreeze
}

// freezeDispatch is a freeze or unfreeze the dashboard dispatched whose run
// GitHub does not list yet.
type freezeDispatch struct {
	action, email string
	at            time.Time // to the second, as GitHub's run times are
}

// ConflictError is a refusal with a message fit to show to the user. It is an
// ErrConflict.
type ConflictError struct{ Msg string }

func (e *ConflictError) Error() string { return e.Msg }
func (e *ConflictError) Unwrap() error { return ErrConflict }

// InstanceFreeze is the freeze state of a unit on one OBS instance.
type InstanceFreeze struct {
	Instance string `json:"instance"`
	State    string `json:"state"`
}

// FreezeSource reads the freeze state of a unit from every instance.
type FreezeSource interface {
	UnitFreeze(ctx context.Context, unit string) []InstanceFreeze
}

// FreezeRun is a freeze or unfreeze run that has not finished.
type FreezeRun struct {
	Action string `json:"action"`
	URL    string `json:"url"`
	By     string `json:"by"`
}

// FreezeFailure is a freeze or unfreeze run that failed on some instances.
type FreezeFailure struct {
	Action    string   `json:"action"`
	Instances []string `json:"instances"`
	URL       string   `json:"url"`
}

// UnitFreeze is the freeze facts of one release unit.
type UnitFreeze struct {
	Unit      string           `json:"unit"`
	State     string           `json:"state"`
	Instances []InstanceFreeze `json:"instances"`
	FrozenBy  string           `json:"frozen_by"`
	FrozenAt  *time.Time       `json:"frozen_at"`
	Pending   *FreezeRun       `json:"pending"`
	Failed    *FreezeFailure   `json:"failed"`
}

// unitFreezeState folds the instance states of a unit into one state. An
// unknown instance does not count: it was not read.
func unitFreezeState(inst []InstanceFreeze) string {
	var frozen, unfrozen int
	for _, i := range inst {
		switch i.State {
		case "frozen":
			frozen++
		case "unfrozen":
			unfrozen++
		case "partial":
			return "partial"
		}
	}
	switch {
	case frozen > 0 && unfrozen == 0:
		return "frozen"
	case unfrozen > 0 && frozen == 0:
		return "unfrozen"
	case frozen > 0:
		return "partial"
	}
	return ""
}

// latestFreezeRuns maps each unit to the newest freeze or unfreeze run that
// names it. Runs whose name does not parse are ignored.
func latestFreezeRuns(runs []Run) map[string]Run {
	latest := map[string]Run{}
	for _, r := range runs {
		_, units, ok := ParseFreezeRunName(r.Name)
		if !ok {
			continue
		}
		for _, u := range units {
			if cur, seen := latest[u]; !seen || r.CreatedAt.After(cur.CreatedAt) {
				latest[u] = r
			}
		}
	}
	return latest
}

// runInProgress reports whether a run has not completed yet.
func runInProgress(r Run) bool { return r.Status != "completed" }

// FreezeStates returns the freeze facts of every unit of the units file, in
// file order: the state read from OBS, who froze it, and the run in flight or
// that failed.
func (s *Service) FreezeStates(ctx context.Context) ([]UnitFreeze, error) {
	units, err := s.mainUnits(ctx)
	if err != nil {
		return nil, err
	}
	runs, err := s.gh.WorkflowRuns(ctx, WorkflowFreeze, runListLimit)
	if err != nil {
		return nil, err
	}
	latest := latestFreezeRuns(runs)
	audit, err := s.latestFreezeAudit()
	if err != nil {
		return nil, err
	}
	s.noteFreezeRuns(runs)

	names := make([]string, len(units))
	for i, u := range units {
		names[i] = u.def.Unit
	}
	inst := s.freezeInstances(ctx, names)

	jobs := map[int64][]Job{}
	out := make([]UnitFreeze, 0, len(units))
	for _, name := range names {
		uf := UnitFreeze{Unit: name, Instances: inst[name], State: unitFreezeState(inst[name])}
		row, haveRow := audit[name]
		if haveRow && row.Action == auditFreeze && (uf.State == "frozen" || uf.State == "partial") {
			at := row.At
			uf.FrozenBy, uf.FrozenAt = row.Email, &at
		}
		run, haveRun := latest[name]
		switch {
		case haveRun && runInProgress(run):
			action, _, _ := ParseFreezeRunName(run.Name)
			p := &FreezeRun{Action: action, URL: run.HTMLURL}
			if haveRow && row.Action == action && absDuration(row.At.Sub(run.CreatedAt)) <= recentCutTTL {
				p.By = row.Email
			}
			uf.Pending = p
		default:
			if d, ok := s.pendingFreezeOf(name, runs); ok {
				uf.Pending = &FreezeRun{Action: d.action, By: d.email}
				break
			}
			if haveRun && run.Conclusion != "success" {
				action, _, _ := ParseFreezeRunName(run.Name)
				js, seen := jobs[run.ID]
				if !seen {
					var jerr error
					if js, jerr = s.gh.Jobs(ctx, run.ID); jerr != nil {
						slog.Warn("release: freeze run jobs", "run", run.ID, "err", jerr)
						js = nil
					}
					jobs[run.ID] = js
				}
				failed := []string{}
				for _, j := range js {
					in, ok := InstanceOf(j.Name, FreezeJobPrefix(action))
					if ok && j.Conclusion != "success" && j.Conclusion != "skipped" {
						failed = append(failed, in)
					}
				}
				uf.Failed = &FreezeFailure{Action: action, Instances: failed, URL: run.HTMLURL}
			}
		}
		out = append(out, uf)
	}
	return out, nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// latestFreezeAudit returns, for each unit, the newest freeze or unfreeze
// audit row naming it.
func (s *Service) latestFreezeAudit() (map[string]store.ReleaseAudit, error) {
	out := map[string]store.ReleaseAudit{}
	if s.db == nil {
		return out, nil
	}
	rows, err := store.ReleaseAuditSince(s.db, []string{auditFreeze, auditUnfreeze}, time.Time{})
	if err != nil {
		return nil, err
	}
	for _, r := range rows { // oldest first
		for _, u := range strings.Fields(r.Detail) {
			out[u] = r
		}
	}
	return out, nil
}

// noteFreezeRuns drops the cached OBS reads when a run seen in progress on the
// previous read is no longer in progress: it has changed what OBS says.
func (s *Service) noteFreezeRuns(runs []Run) {
	s.freezeMu.Lock()
	defer s.freezeMu.Unlock()
	now := map[int64]bool{}
	for _, r := range runs {
		if runInProgress(r) {
			now[r.ID] = true
		}
	}
	for id := range s.freezeRunning {
		if !now[id] {
			s.dropFreezeMetaLocked()
			break
		}
	}
	s.freezeRunning = now
}

func (s *Service) dropFreezeMetaLocked() {
	s.freezeMeta = nil
	s.freezeGen++
}

// pendingFreezeOf returns the freeze dispatched for unit that is still
// awaited. The marker goes once a run naming the unit, created at or after the
// dispatch, is listed, or after recentCutTTL.
func (s *Service) pendingFreezeOf(unit string, runs []Run) (freezeDispatch, bool) {
	s.freezeMu.Lock()
	defer s.freezeMu.Unlock()
	d, ok := s.freezePending[unit]
	if !ok {
		return freezeDispatch{}, false
	}
	gone := s.now().Sub(d.at) >= recentCutTTL
	for _, r := range runs {
		if r.CreatedAt.Before(d.at) {
			continue
		}
		if _, us, ok := ParseFreezeRunName(r.Name); ok {
			for _, u := range us {
				if u == unit {
					gone = true
				}
			}
		}
	}
	if gone {
		delete(s.freezePending, unit)
		return freezeDispatch{}, false
	}
	return d, true
}

// freezeInstances returns the OBS freeze state of each unit, reusing reads
// younger than freezeMetaTTL. Units are read concurrently.
func (s *Service) freezeInstances(ctx context.Context, units []string) map[string][]InstanceFreeze {
	out := make(map[string][]InstanceFreeze, len(units))
	if s.freezeSrc == nil {
		for _, u := range units {
			out[u] = []InstanceFreeze{}
		}
		return out
	}
	s.freezeMu.Lock()
	gen := s.freezeGen
	var missing []string
	for _, u := range units {
		if e, ok := s.freezeMeta[u]; ok && s.now().Sub(e.at) < freezeMetaTTL {
			out[u] = e.inst
		} else {
			missing = append(missing, u)
		}
	}
	s.freezeMu.Unlock()

	if len(missing) == 0 {
		return out
	}

	// One refresh at a time: a caller that waited takes what the running one
	// stored.
	s.freezeRefreshMu.Lock()
	defer s.freezeRefreshMu.Unlock()
	s.freezeMu.Lock()
	gen = s.freezeGen
	still := missing[:0:0]
	for _, u := range missing {
		if e, ok := s.freezeMeta[u]; ok && s.now().Sub(e.at) < freezeMetaTTL {
			out[u] = e.inst
		} else {
			still = append(still, u)
		}
	}
	s.freezeMu.Unlock()
	missing = still
	if len(missing) == 0 {
		return out
	}

	read := make([][]InstanceFreeze, len(missing))
	sem := make(chan struct{}, freezeReadConcurrency)
	var wg sync.WaitGroup
	for i, u := range missing {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			inst := s.freezeSrc.UnitFreeze(ctx, u)
			if inst == nil {
				inst = []InstanceFreeze{}
			}
			read[i] = inst
		}()
	}
	wg.Wait()

	s.freezeMu.Lock()
	defer s.freezeMu.Unlock()
	if s.freezeMeta == nil {
		s.freezeMeta = map[string]freezeMetaEntry{}
	}
	// A read made under a cancelled context may be partial: do not keep it.
	keep := gen == s.freezeGen && ctx.Err() == nil
	for i, u := range missing {
		out[u] = read[i]
		if keep {
			s.freezeMeta[u] = freezeMetaEntry{at: s.now(), inst: read[i]}
		}
	}
	return out
}

// SetFreeze dispatches the freeze or unfreeze of units (unit roots) and logs
// it. A unit that has a freeze or unfreeze in flight is refused.
func (s *Service) SetFreeze(ctx context.Context, email, action string, units []string) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	if len(units) == 0 {
		return fmt.Errorf("%w: choose at least one unit", ErrBadRequest)
	}
	if action != ActionFreeze && action != ActionUnfreeze {
		return fmt.Errorf("%w: unknown action %q", ErrBadRequest, action)
	}
	states, err := s.FreezeStates(ctx)
	if err != nil {
		return err
	}
	byUnit := map[string]UnitFreeze{}
	for _, st := range states {
		byUnit[st.Unit] = st
	}
	seen := map[string]bool{}
	for _, u := range units {
		st, ok := byUnit[u]
		switch {
		case !ok:
			return fmt.Errorf("%w: unknown unit %s", ErrBadRequest, u)
		case seen[u]:
			return fmt.Errorf("%w: unit %s is chosen twice", ErrBadRequest, u)
		case st.Pending != nil:
			return &ConflictError{Msg: fmt.Sprintf("unit %s has a %s in progress", u, st.Pending.Action)}
		}
		seen[u] = true
	}
	joined := strings.Join(units, " ")
	at := s.now().UTC().Truncate(time.Second)
	if err := s.gh.Dispatch(ctx, WorkflowFreeze, map[string]string{"units": joined, "action": action}); err != nil {
		return err
	}
	s.freezeMu.Lock()
	if s.freezePending == nil {
		s.freezePending = map[string]freezeDispatch{}
	}
	for _, u := range units {
		s.freezePending[u] = freezeDispatch{action: action, email: email, at: at}
	}
	s.dropFreezeMetaLocked()
	s.freezeMu.Unlock()
	s.recordRow(email, action, "", 0, joined)
	return nil
}
