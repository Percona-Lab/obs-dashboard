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
	auditRecut            = "recut"
	auditRetryRelease     = "retry_release"
	auditAbandon          = "abandon"
)

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

func atChangelog(r Release) bool { return r.Step == StepChangelog && r.PR != 0 && cutIdle(r) }

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
	if len(units) == 0 {
		return "", fmt.Errorf("%w: choose at least one unit", ErrBadRequest)
	}
	s.Invalidate()
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
		seen[u] = true
	}
	branch := BranchPrefix + s.now().UTC().Format("20060102T150405")
	joined := strings.Join(units, " ")
	if err := s.gh.Dispatch(ctx, WorkflowCut, map[string]string{"units": joined, "branch": branch}); err != nil {
		return "", err
	}
	s.recordRow(email, auditCut, branch, 0, joined)
	s.Invalidate()
	return strings.TrimPrefix(branch, BranchPrefix), nil
}

// SaveChangelog replaces unit's changelog section with section by amending
// the release commit. It returns the new head.
func (s *Service) SaveChangelog(ctx context.Context, email, id, head, unit, section string) (string, error) {
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
	if errors.Is(err, ErrNotFound) {
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
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		return r.Step == StepSync && r.PR != 0 && r.SyncState == string(StateFailure)
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
	s.record(email, auditRetrySync, r, "")
	s.Invalidate()
	return nil
}

// ApproveQA starts the QA lanes.
func (s *Service) ApproveQA(ctx context.Context, email, id, head string) error {
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

// Recut rebases the release on the latest main and runs every workflow again:
// both labels come off and the cut is dispatched anew. It also retries a cut
// that failed.
func (s *Service) Recut(ctx context.Context, email, id, head string) error {
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		if !ownsBranch(r) {
			return false
		}
		switch r.Step {
		case StepChangelog, StepSync, StepQA:
			return true
		case StepCutting:
			// A cut that is queued or running is not ours to repeat.
			return r.Cut != nil && cutIdle(r)
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
	if err := s.gh.Dispatch(ctx, WorkflowCut, map[string]string{"units": joined, "branch": r.Branch}); err != nil {
		return err
	}
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

// Release merges the release PR; the release workflow then publishes it. A
// failed QA lane needs confirmFailed.
func (s *Service) Release(ctx context.Context, email, id, head string, confirmFailed bool) error {
	r, err := s.guard(ctx, id, head, func(r Release) bool {
		return r.Step == StepQA && r.PR != 0 && r.QA != nil && r.QA.State == "finished"
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
	r, err := s.guardAny(ctx, id, func(r Release) bool {
		return r.Step == StepReleasing && r.PR != 0 && r.Final != nil && r.Final.State == StateFailure
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
	s.record(email, auditRetryRelease, r, "")
	s.Invalidate()
	return nil
}

// Abandon gives up a release that is not merged: the branch goes (which makes
// GitHub close the PR) and the PR is closed explicitly too. A PR made by hand
// is only closed; its branch is not ours.
func (s *Service) Abandon(ctx context.Context, email, id, head string) error {
	r, err := s.guard(ctx, id, head, func(r Release) bool {
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
