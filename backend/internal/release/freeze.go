package release

import (
	"context"
	"time"
)

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
