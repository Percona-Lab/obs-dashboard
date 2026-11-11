package release

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const fakeRepo = "percona/obs-packaging"

// fakeGitHub is an in-memory GitHub. Reads come from the exported-looking
// fields below; writes change them the way GitHub would, so a later read sees
// the effect. Every call is appended to calls as "Method arg arg…". When err
// is set, every method returns it.
type fakeGitHub struct {
	mu sync.Mutex

	open        []Pull
	merged      []Pull
	pullFiles   map[int][]string
	branches    []Ref
	files       map[string][]byte // "ref:path"
	runs        []Run             // all workflows; filtered by WorkflowFile / HeadSHA
	jobs        map[int64][]Job   // by run id
	annotations map[int64][]Annotation
	artifacts   map[string][]byte // "runID:name"
	releases    []GHRelease
	tagSHAs     map[string]string
	commitPulls map[string]Pull // sha -> PR (Number, HTMLURL)
	closedHeads map[string]bool // branches that have a closed pull request

	calls        []string
	err          error
	hasClosedErr error            // returned by HasClosedPull only
	deleteErr    error            // returned by DeleteBranch only
	closeErr     error            // returned by ClosePull only
	fileErrs     map[string]error // "ref:path" -> error File returns for it
	nextPR       int
	block        chan struct{} // when non-nil, Branches waits for it to close
	now          func() time.Time
}

var _ GitHub = (*fakeGitHub)(nil)

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{
		pullFiles:   map[int][]string{},
		files:       map[string][]byte{},
		jobs:        map[int64][]Job{},
		annotations: map[int64][]Annotation{},
		artifacts:   map[string][]byte{},
		tagSHAs:     map[string]string{},
		commitPulls: map[string]Pull{},
		closedHeads: map[string]bool{},
		fileErrs:    map[string]error{},
		nextPR:      500,
		now:         time.Now,
	}
}

func fakeNotFound(what string) error {
	return &APIError{Method: "GET", Path: "/fake/" + what, Status: 404, kind: ErrNotFound}
}

func fakeConflict(what string) error {
	return &APIError{Method: "PUT", Path: "/fake/" + what, Status: 409, kind: ErrConflict}
}

// record logs the call and returns the injected error, if any. Callers hold
// no lock; record takes it and keeps it: the caller must unlock.
func (f *fakeGitHub) record(method string, args ...any) error {
	f.mu.Lock()
	parts := []string{method}
	for _, a := range args {
		parts = append(parts, fmt.Sprint(a))
	}
	f.calls = append(f.calls, strings.Join(parts, " "))
	return f.err
}

// setFile stores a file at ref.
func (f *fakeGitHub) setFile(ref, path, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[ref+":"+path] = []byte(content)
}

// setErr sets or clears the error every method returns.
func (f *fakeGitHub) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// callsOf returns the logged calls whose method is method.
func (f *fakeGitHub) callsOf(method string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c == method || strings.HasPrefix(c, method+" ") {
			out = append(out, c)
		}
	}
	return out
}

// allCalls returns a copy of the call log.
func (f *fakeGitHub) allCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeGitHub) openIndex(number int) int {
	for i := range f.open {
		if f.open[i].Number == number {
			return i
		}
	}
	return -1
}

func (f *fakeGitHub) OpenPulls(ctx context.Context) ([]Pull, error) {
	err := f.record("OpenPulls")
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([]Pull, len(f.open))
	for i, p := range f.open {
		p.Labels = append([]string(nil), p.Labels...)
		out[i] = p
	}
	return out, nil
}

func (f *fakeGitHub) MergedPulls(ctx context.Context, since time.Time) ([]Pull, error) {
	err := f.record("MergedPulls")
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []Pull
	for _, p := range f.merged {
		if p.MergedAt != nil && !p.MergedAt.Before(since) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeGitHub) HasClosedPull(ctx context.Context, branch string) (bool, error) {
	err := f.record("HasClosedPull", branch)
	defer f.mu.Unlock()
	if err == nil {
		err = f.hasClosedErr
	}
	if err != nil {
		return false, err
	}
	return f.closedHeads[branch], nil
}

func (f *fakeGitHub) PullFiles(ctx context.Context, number int) ([]string, error) {
	err := f.record("PullFiles", number)
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), f.pullFiles[number]...), nil
}

func (f *fakeGitHub) CreatePull(ctx context.Context, head, title, body string) (Pull, error) {
	err := f.record("CreatePull", head, title, body)
	defer f.mu.Unlock()
	if err != nil {
		return Pull{}, err
	}
	for _, p := range f.open {
		if p.HeadRef == head && p.HeadRepo == p.BaseRepo {
			return Pull{}, &APIError{Method: "POST", Path: "/fake/pulls", Status: 422}
		}
	}
	f.nextPR++
	p := Pull{
		Number: f.nextPR, Title: title, HeadRef: head, State: "open",
		HeadRepo: fakeRepo, BaseRepo: fakeRepo, CreatedAt: f.now(),
		HTMLURL: fmt.Sprintf("https://github.com/%s/pull/%d", fakeRepo, f.nextPR),
	}
	for _, b := range f.branches {
		if b.Name == head {
			p.HeadSHA = b.SHA
		}
	}
	f.open = append(f.open, p)
	return p, nil
}

func (f *fakeGitHub) ClosePull(ctx context.Context, number int) error {
	err := f.record("ClosePull", number)
	defer f.mu.Unlock()
	if err != nil {
		return err
	}
	if f.closeErr != nil {
		return f.closeErr
	}
	if i := f.openIndex(number); i >= 0 {
		f.open = append(f.open[:i], f.open[i+1:]...)
	}
	return nil
}

func (f *fakeGitHub) MergePull(ctx context.Context, number int, headSHA string) error {
	err := f.record("MergePull", number, headSHA)
	defer f.mu.Unlock()
	if err != nil {
		return err
	}
	i := f.openIndex(number)
	if i < 0 {
		return fakeNotFound("pull")
	}
	p := f.open[i]
	if p.HeadSHA != headSHA {
		return fakeConflict("merge")
	}
	t := f.now().UTC()
	p.MergedAt = &t
	p.State = "closed"
	p.MergeSHA = p.HeadSHA
	f.open = append(f.open[:i], f.open[i+1:]...)
	f.merged = append(f.merged, p)
	return nil
}

func (f *fakeGitHub) AddLabel(ctx context.Context, number int, label string) error {
	err := f.record("AddLabel", number, label)
	defer f.mu.Unlock()
	if err != nil {
		return err
	}
	i := f.openIndex(number)
	if i < 0 {
		return fakeNotFound("pull")
	}
	for _, l := range f.open[i].Labels {
		if l == label {
			return nil
		}
	}
	f.open[i].Labels = append(f.open[i].Labels, label)
	return nil
}

func (f *fakeGitHub) RemoveLabel(ctx context.Context, number int, label string) error {
	err := f.record("RemoveLabel", number, label)
	defer f.mu.Unlock()
	if err != nil {
		return err
	}
	i := f.openIndex(number)
	if i < 0 {
		return nil
	}
	var keep []string
	for _, l := range f.open[i].Labels {
		if l != label {
			keep = append(keep, l)
		}
	}
	f.open[i].Labels = keep
	return nil
}

func (f *fakeGitHub) Branches(ctx context.Context, prefix string) ([]Ref, error) {
	err := f.record("Branches", prefix)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Ref
	for _, b := range f.branches {
		if strings.HasPrefix(b.Name, prefix) {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeGitHub) DeleteBranch(ctx context.Context, branch string) error {
	err := f.record("DeleteBranch", branch)
	defer f.mu.Unlock()
	if err != nil {
		return err
	}
	if f.deleteErr != nil {
		return f.deleteErr
	}
	var keep []Ref
	for _, b := range f.branches {
		if b.Name != branch {
			keep = append(keep, b)
		}
	}
	f.branches = keep
	return nil
}

func (f *fakeGitHub) File(ctx context.Context, path, ref string) ([]byte, error) {
	err := f.record("File", path, ref)
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if e := f.fileErrs[ref+":"+path]; e != nil {
		return nil, e
	}
	b, ok := f.files[ref+":"+path]
	if !ok {
		return nil, fakeNotFound("contents/" + path)
	}
	return append([]byte(nil), b...), nil
}

// AmendFile replaces the file on branch and gives the branch (and the open PR
// on it) a new head sha. The files of the old head stay readable under the
// branch name only.
func (f *fakeGitHub) AmendFile(ctx context.Context, branch, expectHead, path string, content []byte) (string, error) {
	err := f.record("AmendFile", branch, expectHead, path, string(content))
	defer f.mu.Unlock()
	if err != nil {
		return "", err
	}
	bi := -1
	for i := range f.branches {
		if f.branches[i].Name == branch {
			bi = i
		}
	}
	if bi < 0 {
		return "", fakeNotFound("branch")
	}
	if f.branches[bi].SHA != expectHead {
		return "", fakeConflict("ref")
	}
	sha := fmt.Sprintf("%s-amended%d", expectHead, len(f.calls))
	f.branches[bi].SHA = sha
	f.files[branch+":"+path] = append([]byte(nil), content...)
	for i := range f.open {
		if f.open[i].HeadRef == branch && f.open[i].HeadRepo == f.open[i].BaseRepo {
			f.open[i].HeadSHA = sha
		}
	}
	return sha, nil
}

func (f *fakeGitHub) Dispatch(ctx context.Context, workflow string, inputs map[string]string) error {
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := []any{workflow}
	for _, k := range keys {
		args = append(args, k+"="+inputs[k])
	}
	err := f.record("Dispatch", args...)
	defer f.mu.Unlock()
	return err
}

func (f *fakeGitHub) WorkflowRuns(ctx context.Context, workflow string, limit int) ([]Run, error) {
	err := f.record("WorkflowRuns", workflow)
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []Run
	for _, r := range f.runs {
		if r.WorkflowFile == workflow {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeGitHub) RunsForSHA(ctx context.Context, sha string) ([]Run, error) {
	err := f.record("RunsForSHA", sha)
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []Run
	for _, r := range f.runs {
		if r.HeadSHA == sha {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeGitHub) Jobs(ctx context.Context, runID int64) ([]Job, error) {
	err := f.record("Jobs", runID)
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return append([]Job(nil), f.jobs[runID]...), nil
}

func (f *fakeGitHub) Annotations(ctx context.Context, jobID int64) ([]Annotation, error) {
	err := f.record("Annotations", jobID)
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return append([]Annotation(nil), f.annotations[jobID]...), nil
}

func (f *fakeGitHub) Artifact(ctx context.Context, runID int64, name string) ([]byte, error) {
	err := f.record("Artifact", runID, name)
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	b, ok := f.artifacts[fmt.Sprintf("%d:%s", runID, name)]
	if !ok {
		return nil, fakeNotFound("artifacts")
	}
	return append([]byte(nil), b...), nil
}

func (f *fakeGitHub) Releases(ctx context.Context, page, perPage int) ([]GHRelease, error) {
	err := f.record("Releases", page, perPage)
	defer f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	start := (page - 1) * perPage
	if start < 0 || start >= len(f.releases) {
		return nil, nil
	}
	end := min(start+perPage, len(f.releases))
	return append([]GHRelease(nil), f.releases[start:end]...), nil
}

func (f *fakeGitHub) TagSHA(ctx context.Context, tag string) (string, error) {
	err := f.record("TagSHA", tag)
	defer f.mu.Unlock()
	if err != nil {
		return "", err
	}
	sha, ok := f.tagSHAs[tag]
	if !ok {
		return "", fakeNotFound("tag")
	}
	return sha, nil
}

func (f *fakeGitHub) PullForCommit(ctx context.Context, sha string) (int, string, error) {
	err := f.record("PullForCommit", sha)
	defer f.mu.Unlock()
	if err != nil {
		return 0, "", err
	}
	p := f.commitPulls[sha]
	return p.Number, p.HTMLURL, nil
}
