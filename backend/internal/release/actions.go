package release

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

// ErrBadRequest marks input the user can fix. Its text is short and meant to
// be shown to them.
var ErrBadRequest = errors.New("bad request")

// errStale is returned when current GitHub state cannot be read: an action
// is never decided on a cached result that is known to be out of date.
var errStale = errors.New("release state is unavailable, try again in a moment")

// Audit actions of the wizard buttons.
const (
	auditSaveChangelog    = "save_changelog"
	auditApproveChangelog = "approve_changelog"
	auditRetrySync        = "retry_sync"
	auditApproveQA        = "approve_qa"
	auditRetryQA          = "retry_qa"
	auditSkipQA           = "skip_qa"  // Detail is the PR head the skip is for
	auditRerunQA          = "rerun_qa" // Detail is the lanes run again, one per line
	auditRecut            = "recut"
	auditRetryRelease     = "retry_release"
	auditAbandon          = "abandon"
)

// recentCutTTL is how long a dispatched cut counts as in flight before GitHub
// is expected to show it.
const recentCutTTL = 2 * time.Minute

// retryCooldown is how long a retry is not accepted again for the same
// release: GitHub needs a moment to show the run the first one started.
const retryCooldown = time.Minute

// pendingCut is a cut the dashboard dispatched whose run GitHub does not list
// yet. Until it does (or recentCutTTL passes) the release reads as cutting.
type pendingCut struct {
	at    time.Time // dispatch time, to the second as GitHub's run times are
	units []string
}

// notePendingCut remembers a cut dispatched for branch at time at.
func (s *Service) notePendingCut(branch string, at time.Time, units []string) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.pending == nil {
		s.pending = map[string]pendingCut{}
	}
	s.pending[branch] = pendingCut{at: at, units: append([]string(nil), units...)}
}

// pendingCutOf returns the cut dispatched for branch that is still awaited.
// The marker goes once a cut run created at or after the dispatch is listed,
// or after recentCutTTL.
func (s *Service) pendingCutOf(branch string, cutRuns []Run) (pendingCut, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	p, ok := s.pending[branch]
	if !ok {
		return pendingCut{}, false
	}
	gone := s.now().Sub(p.at) >= recentCutTTL
	name := CutRunName(branch)
	for _, r := range cutRuns {
		if r.Name == name && !r.CreatedAt.Before(p.at) {
			gone = true
		}
	}
	if gone {
		delete(s.pending, branch)
		return pendingCut{}, false
	}
	return p, true
}

// pendingBranches returns the branches with a cut still awaited.
func (s *Service) pendingBranches(cutRuns []Run) []string {
	s.cacheMu.Lock()
	names := make([]string, 0, len(s.pending))
	for b := range s.pending {
		names = append(names, b)
	}
	s.cacheMu.Unlock()
	sort.Strings(names)
	var out []string
	for _, b := range names {
		if _, ok := s.pendingCutOf(b, cutRuns); ok {
			out = append(out, b)
		}
	}
	return out
}

// retriedRecently refuses a retry that was accepted for the same release less
// than retryCooldown ago. s.actMu must be held.
func (s *Service) retriedRecently(action, id string) error {
	if at, ok := s.lastRetry[action+" "+id]; ok && s.now().Sub(at) < retryCooldown {
		return fmt.Errorf("%w: this was retried a moment ago, give it a minute", ErrConflict)
	}
	return nil
}

// noteRetry records an accepted retry. s.actMu must be held.
func (s *Service) noteRetry(action, id string) {
	now := s.now()
	if s.lastRetry == nil {
		s.lastRetry = map[string]time.Time{}
	}
	for k, at := range s.lastRetry {
		if now.Sub(at) >= retryCooldown {
			delete(s.lastRetry, k)
		}
	}
	s.lastRetry[action+" "+id] = now
}

// guardAny reads GitHub afresh and returns the release with id when it is a
// dashboard release (not a fork PR) for which allowed holds. The email of the
// actor never gets here: it is for the audit log only.
func (s *Service) guardAny(ctx context.Context, id string, allowed func(Release) bool) (Release, error) {
	s.Invalidate()
	snap, err := s.Snapshot(ctx)
	if err != nil {
		return Release{}, err
	}
	if snap.Stale {
		return Release{}, errStale
	}
	for _, r := range snap.Releases {
		if r.ID != id {
			continue
		}
		if !r.Dashboard {
			return Release{}, fmt.Errorf("%w: this release comes from a fork and cannot be changed here", ErrConflict)
		}
		if r.pending {
			return Release{}, fmt.Errorf("%w: a cut was just started for this release, wait for it", ErrConflict)
		}
		if !allowed(r) && cutHeld(r) {
			return Release{}, fmt.Errorf("%w: the cut of this release is waiting for approval on GitHub", ErrConflict)
		}
		if !allowed(r) {
			return Release{}, fmt.Errorf("%w: this release is at step %q, which does not allow that action", ErrConflict, r.Step)
		}
		return r, nil
	}
	return Release{}, fmt.Errorf("%w: release %s", ErrNotFound, id)
}

// guard is guardAny plus the check that the head the client saw is still the
// head on GitHub.
func (s *Service) guard(ctx context.Context, id, head string, allowed func(Release) bool) (Release, error) {
	r, err := s.guardAny(ctx, id, allowed)
	if err != nil {
		return Release{}, err
	}
	if r.HeadSHA != head {
		return Release{}, fmt.Errorf("%w: the release changed since you loaded it, reload and try again", ErrConflict)
	}
	return r, nil
}

func cutIdle(r Release) bool {
	return r.Cut == nil || (r.Cut.State != StateQueued && r.Cut.State != StateRunning)
}

// cutHeld reports whether the cut of r waits for approval on GitHub. The
// release is then at the cutting step, which no action but Abandon accepts.
func cutHeld(r Release) bool { return r.Cut != nil && r.Cut.State == StateApproval }

// atChangelog reports whether the changelog may be edited and approved. After
// a failed re-cut the PR still shows the release as it was before: it stays
// locked until a cut succeeds.
func atChangelog(r Release) bool {
	return r.Step == StepChangelog && r.PR != 0 && (r.Cut == nil || r.Cut.State == StateSuccess)
}

// ownsBranch reports whether the release branch was made by the dashboard
// and so may be deleted by it.
func ownsBranch(r Release) bool {
	return !strings.HasPrefix(r.ID, "pr-") && strings.HasPrefix(r.Branch, BranchPrefix)
}

// record appends the audit row of a successful action. The action has
// already happened, so a failure to log it is reported but does not fail it.
func (s *Service) record(email, action string, r Release, detail string) {
	s.recordRow(email, action, r.Branch, r.PR, detail)
}

func (s *Service) recordRow(email, action, branch string, pr int, detail string) {
	if s.db == nil {
		return
	}
	err := store.AddReleaseAudit(s.db, store.ReleaseAudit{
		At: s.now().UTC(), Email: email, Action: action, Branch: branch, PR: pr, Detail: detail,
	})
	if err != nil {
		slog.Error("release: write audit row", "action", action, "branch", branch, "err", err)
	}
}

// Cut starts a release of units (unit roots, as listed by Units). Each must
// be tickable now. It returns the id of the new release.
func (s *Service) Cut(ctx context.Context, email string, units []string) (string, error) {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	if len(units) == 0 {
		return "", fmt.Errorf("%w: choose at least one unit", ErrBadRequest)
	}
	s.Invalidate()
	snap, err := s.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	if snap.Stale {
		return "", errStale
	}
	rows, err := s.Units(ctx)
	if err != nil {
		return "", err
	}
	byUnit := map[string]UnitRow{}
	for _, r := range rows {
		byUnit[r.Unit] = r
	}
	seen := map[string]bool{}
	for _, u := range units {
		row, ok := byUnit[u]
		switch {
		case !ok:
			return "", fmt.Errorf("%w: unknown unit %s", ErrBadRequest, u)
		case seen[u]:
			return "", fmt.Errorf("%w: unit %s is chosen twice", ErrBadRequest, u)
		case !row.Tickable:
			why := "it cannot be released now"
			if row.Blocked != nil {
				why = row.Blocked.Text
			}
			return "", fmt.Errorf("%w: unit %s: %s", ErrConflict, u, why)
		}
		if at, ok := s.recentCuts[u]; ok && s.now().Sub(at) < recentCutTTL {
			return "", fmt.Errorf("%w: unit %s was cut a moment ago", ErrConflict, u)
		}
		seen[u] = true
	}
	// Branch names are second-granular: never reuse the previous one.
	ts := s.now().UTC().Truncate(time.Second)
	if !ts.After(s.lastCutAt) {
		ts = s.lastCutAt.Add(time.Second)
	}
	branch := BranchPrefix + ts.Format("20060102T150405")
	joined := strings.Join(units, " ")
	dispatchedAt := s.now().UTC().Truncate(time.Second)
	if err := s.gh.Dispatch(ctx, WorkflowCut, map[string]string{"units": joined, "branch": branch}); err != nil {
		return "", err
	}
	s.lastCutAt = ts
	if s.recentCuts == nil {
		s.recentCuts = map[string]time.Time{}
	}
	now := s.now()
	for u, at := range s.recentCuts {
		if now.Sub(at) >= recentCutTTL {
			delete(s.recentCuts, u)
		}
	}
	for _, u := range units {
		s.recentCuts[u] = now
	}
	s.notePendingCut(branch, dispatchedAt, units)
	s.recordRow(email, auditCut, branch, 0, joined)
	s.Invalidate()
	return strings.TrimPrefix(branch, BranchPrefix), nil
}

// SaveChangelog replaces unit's changelog section with section by amending
// the release commit. It returns the new head.
func (s *Service) SaveChangelog(ctx context.Context, email, id, head, unit, section string) (string, error) {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	r, err := s.guard(ctx, id, head, atChangelog)
	if err != nil {
		return "", err
	}
	newHead, err := s.writeSection(ctx, r, unit, func(UnitState) (string, error) { return section, nil })
	if err != nil {
		return "", err
	}
	s.record(email, auditSaveChangelog, r, unit)
	s.Invalidate()
	return newHead, nil
}

// ResetChangelog puts back the changelog section the cut generated for unit.
func (s *Service) ResetChangelog(ctx context.Context, email, id, head, unit string) (string, error) {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	r, err := s.guard(ctx, id, head, atChangelog)
	if err != nil {
		return "", err
	}
	newHead, err := s.writeSection(ctx, r, unit, func(u UnitState) (string, error) {
		if u.Generated == nil {
			return "", fmt.Errorf("%w: no generated changelog is available for %s", ErrBadRequest, unit)
		}
		return *u.Generated, nil
	})
	if err != nil {
		return "", err
	}
	s.record(email, auditSaveChangelog, r, unit+" (reset)")
	s.Invalidate()
	return newHead, nil
}

// writeSection amends the CHANGELOG.md of unit on the release branch with the
// section text pick returns.
func (s *Service) writeSection(ctx context.Context, r Release, unit string, pick func(UnitState) (string, error)) (string, error) {
	var u *UnitState
	for i := range r.Units {
		if r.Units[i].Unit == unit {
			u = &r.Units[i]
		}
	}
	if u == nil || u.ReleaseID == "" || u.ReleaseDir == "" {
		return "", fmt.Errorf("%w: unknown unit %s", ErrBadRequest, unit)
	}
	section, err := pick(*u)
	if err != nil {
		return "", err
	}
	file := path.Join(u.ReleaseDir, "CHANGELOG.md")
	current, err := s.gh.File(ctx, file, r.Branch)
	if unusable(err) {
		return "", fmt.Errorf("%w: the changelog of %s was not found", ErrBadRequest, unit)
	}
	if err != nil {
		return "", err
	}
	updated, err := ReplaceSection(current, u.ReleaseID, section)
	if err != nil {
		if errors.Is(err, ErrSectionNotFound) || errors.Is(err, ErrSectionAmbiguous) || errors.Is(err, ErrBadSection) {
			return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
		}
		return "", err
	}
	return s.gh.AmendFile(ctx, r.Branch, r.HeadSHA, file, updated)
}

// ApproveChangelog starts the sync: the obs-sync label is what the PR
// workflow waits for.
func (s *Service) ApproveChangelog(ctx context.Context, email, id, head string) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	r, err := s.guard(ctx, id, head, atChangelog)
	if err != nil {
		return err
	}
	if err := s.gh.AddLabel(ctx, r.PR, LabelSync); err != nil {
		return err
	}
	s.record(email, auditApproveChangelog, r, "")
	s.Invalidate()
	return nil
}

// RetrySync runs a failed sync again by taking the obs-sync label off and
// putting it back.
func (s *Service) RetrySync(ctx context.Context, email, id, head string) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	if err := s.retriedRecently(auditRetrySync, id); err != nil {
		return err
	}
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		// A run that is still going would be joined by a second one.
		return r.Step == StepSync && r.PR != 0 && r.SyncState == string(StateFailure) && r.syncRunDone
	})
	if err != nil {
		return err
	}
	if err := s.gh.RemoveLabel(ctx, r.PR, LabelSync); err != nil {
		return err
	}
	if err := s.gh.AddLabel(ctx, r.PR, LabelSync); err != nil {
		return err
	}
	s.noteRetry(auditRetrySync, id)
	s.record(email, auditRetrySync, r, "")
	s.Invalidate()
	return nil
}

// ApproveQA starts the QA lanes.
func (s *Service) ApproveQA(ctx context.Context, email, id, head string) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		return r.Step == StepQA && r.PR != 0 && r.QA != nil && r.QA.State == "waiting"
	})
	if err != nil {
		return err
	}
	if err := s.gh.AddLabel(ctx, r.PR, LabelQA); err != nil {
		return err
	}
	s.record(email, auditApproveQA, r, "")
	s.Invalidate()
	return nil
}

// skipQAComment is left on the release PR when QA is skipped. It does not name
// the user: emails stay in the dashboard.
const skipQAComment = "**QA skipped.** A dashboard user explicitly decided to release commit %s without running QA on it."

// SkipQA lets the release be made without QA having finished for this head of
// the PR. It says so on the PR. A re-cut gives the PR a new head, which the
// skip does not cover.
func (s *Service) SkipQA(ctx context.Context, email, id, head string) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	if s.db == nil {
		return fmt.Errorf("%w: skipping QA needs the database", ErrBadRequest)
	}
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		return r.Step == StepQA && r.PR != 0 && r.QA != nil && r.QA.State != "finished" && !r.QA.Skipped
	})
	if err != nil {
		return err
	}
	if err := s.gh.Comment(ctx, r.PR, fmt.Sprintf(skipQAComment, r.HeadSHA)); err != nil {
		return err
	}
	s.record(email, auditSkipQA, r, r.HeadSHA)
	s.Invalidate()
	return nil
}

// RetryQA starts QA again after it failed to start (the CDN wait or the lane
// matrix failed): the qa-release label comes off and goes back on.
func (s *Service) RetryQA(ctx context.Context, email, id, head string) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	if err := s.retriedRecently(auditRetryQA, id); err != nil {
		return err
	}
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		return r.Step == StepQA && r.PR != 0 && r.QA != nil && r.QA.State == "failure"
	})
	if err != nil {
		return err
	}
	if err := s.gh.RemoveLabel(ctx, r.PR, LabelQA); err != nil {
		return err
	}
	if err := s.gh.AddLabel(ctx, r.PR, LabelQA); err != nil {
		return err
	}
	s.noteRetry(auditRetryQA, id)
	s.record(email, auditRetryQA, r, "")
	s.Invalidate()
	return nil
}

// RerunQA runs failed QA lanes again on the same QA run: the lane named lane,
// or every failed one when lane is "". GitHub runs a job again only once the
// whole run has completed, which is what QA "finished" means.
func (s *Service) RerunQA(ctx context.Context, email, id, head, lane string) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	// GitHub needs a moment to show the run as going again.
	if err := s.retriedRecently(auditRerunQA, id); err != nil {
		return err
	}
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		return r.Step == StepQA && r.PR != 0 && r.QA != nil && r.QA.State == "finished" && r.QA.runID != 0
	})
	if err != nil {
		return err
	}
	var failed []string
	var jobID int64
	for _, l := range r.QA.Lanes {
		if l.State != StateFailure {
			continue
		}
		if lane == "" || l.Name == lane {
			failed = append(failed, JobLanePrefix+l.Name)
			jobID = l.jobID
		}
	}
	switch {
	case len(failed) == 0 && lane == "":
		return fmt.Errorf("%w: no QA lane failed", ErrBadRequest)
	case len(failed) == 0:
		return fmt.Errorf("%w: %s is not a failed QA lane", ErrBadRequest, lane)
	case lane == "":
		err = s.gh.RerunFailedJobs(ctx, r.QA.runID)
	default:
		err = s.gh.RerunJob(ctx, jobID)
	}
	if err != nil {
		return err
	}
	s.noteRetry(auditRerunQA, id)
	s.record(email, auditRerunQA, r, strings.Join(failed, "\n"))
	s.Invalidate()
	return nil
}

// Recut rebases the release on the latest main and runs every workflow again:
// both labels come off and the cut is dispatched anew. It also retries a cut
// that failed.
func (s *Service) Recut(ctx context.Context, email, id, head string) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		if !ownsBranch(r) {
			return false
		}
		switch r.Step {
		case StepChangelog, StepSync, StepQA:
			return true
		case StepCutting:
			// A cut that is queued or running is not ours to repeat, and one
			// that waits for approval is a dispatch already.
			return r.Cut != nil && cutIdle(r) && !cutHeld(r)
		}
		return false
	})
	if err != nil {
		return err
	}
	units, err := s.unitsOf(r)
	if err != nil {
		return err
	}
	if r.PR != 0 {
		for _, label := range []string{LabelSync, LabelQA} {
			if err := s.gh.RemoveLabel(ctx, r.PR, label); err != nil {
				return err
			}
		}
	}
	joined := strings.Join(units, " ")
	dispatchedAt := s.now().UTC().Truncate(time.Second)
	if err := s.gh.Dispatch(ctx, WorkflowCut, map[string]string{"units": joined, "branch": r.Branch}); err != nil {
		return err
	}
	s.notePendingCut(r.Branch, dispatchedAt, units)
	s.record(email, auditRecut, r, joined)
	s.Invalidate()
	return nil
}

// unitsOf returns the unit roots of a release: those its PR or branch holds,
// else those of the newest cut or recut recorded for its branch (a first cut
// that failed has left nothing in GitHub).
func (s *Service) unitsOf(r Release) ([]string, error) {
	var units []string
	for _, u := range r.Units {
		if u.Unit != "" {
			units = append(units, u.Unit)
		}
	}
	if len(units) > 0 {
		return units, nil
	}
	if s.db != nil && r.Branch != "" {
		rows, err := store.ReleaseAuditByBranch(s.db, []string{r.Branch})
		if err != nil {
			return nil, fmt.Errorf("release audit: %w", err)
		}
		list := rows[r.Branch]
		for i := len(list) - 1; i >= 0; i-- {
			if a := list[i]; (a.Action == auditCut || a.Action == auditRecut) && strings.TrimSpace(a.Detail) != "" {
				return strings.Fields(a.Detail), nil
			}
		}
	}
	return nil, fmt.Errorf("%w: the units of this release are not known, abandon it and start again", ErrBadRequest)
}

// Release merges the release PR; the release workflow then publishes it. QA
// must have finished or been skipped. A failed QA lane needs confirmFailed.
func (s *Service) Release(ctx context.Context, email, id, head string, confirmFailed bool) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		return r.Step == StepQA && r.PR != 0 && r.QA != nil && (r.QA.State == "finished" || r.QA.Skipped)
	})
	if err != nil {
		return err
	}
	var failed []string
	for _, l := range r.QA.Lanes {
		if l.State == StateFailure {
			failed = append(failed, JobLanePrefix+l.Name)
		}
	}
	if len(failed) > 0 && !confirmFailed {
		return fmt.Errorf("%w: %d QA lane(s) failed, confirm to release anyway", ErrBadRequest, len(failed))
	}
	if err := s.gh.MergePull(ctx, r.PR, r.HeadSHA); err != nil {
		return err
	}
	s.record(email, auditRelease, r, strings.Join(failed, "\n"))
	if ownsBranch(r) {
		if err := s.gh.DeleteBranch(ctx, r.Branch); err != nil {
			slog.Warn("release: delete merged branch", "branch", r.Branch, "err", err)
		}
	}
	s.Invalidate()
	return nil
}

// RetryRelease runs a failed release workflow again. The PR is merged, so
// there is no head to compare.
func (s *Service) RetryRelease(ctx context.Context, email, id string) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	if err := s.retriedRecently(auditRetryRelease, id); err != nil {
		return err
	}
	r, err := s.guardAny(ctx, id, func(r Release) bool {
		// A run that is still going would be joined by a second one.
		return r.Step == StepReleasing && r.PR != 0 && r.Final != nil && r.Final.State == StateFailure && r.finalRunDone
	})
	if err != nil {
		return err
	}
	tags := append([]string(nil), r.Final.Tags...)
	if len(tags) == 0 {
		return fmt.Errorf("%w: this release has no tags to publish", ErrBadRequest)
	}
	sort.Strings(tags)
	err = s.gh.Dispatch(ctx, WorkflowRelease, map[string]string{
		"pr_number": strconv.Itoa(r.PR), "tags": strings.Join(tags, " "),
	})
	if err != nil {
		return err
	}
	s.noteRetry(auditRetryRelease, id)
	s.record(email, auditRetryRelease, r, "")
	s.Invalidate()
	return nil
}

// Abandon gives up a release that is not merged: the branch goes (which makes
// GitHub close the PR) and the PR is closed explicitly too. A PR made by hand
// is only closed; its branch is not ours.
func (s *Service) Abandon(ctx context.Context, email, id, head string) error {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		if !cutIdle(r) {
			// The running cut would create the branch again.
			return false
		}
		if r.PR == 0 && r.HeadSHA == "" && r.Cut != nil && r.Cut.State == StateFailure {
			// A first cut that failed left no branch and no PR: there is
			// nothing to give up, and the row leaves the list by itself.
			return false
		}
		if cutHeld(r) && r.PR == 0 && r.HeadSHA == "" {
			// The same for a first cut that waits for approval.
			return false
		}
		switch r.Step {
		case StepCutting, StepChangelog, StepSync, StepQA:
			return true
		}
		return false
	})
	if err != nil {
		return err
	}
	own := ownsBranch(r)
	if own {
		if err := s.gh.DeleteBranch(ctx, r.Branch); err != nil {
			return err
		}
	}
	if r.PR != 0 {
		if err := s.gh.ClosePull(ctx, r.PR); err != nil {
			if !own {
				return err
			}
			// Deleting the branch closed the PR already.
			slog.Warn("release: close pull request", "pr", r.PR, "err", err)
		}
	}
	s.record(email, auditAbandon, r, "")
	s.Invalidate()
	return nil
}
