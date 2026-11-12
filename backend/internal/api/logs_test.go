package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
)

type fakeLogSource struct {
	tail []string
	ch   chan string

	mu        sync.Mutex
	cancelled bool
}

func newFakeLogSource(tail ...string) *fakeLogSource {
	return &fakeLogSource{tail: tail, ch: make(chan string, 8)}
}

func (f *fakeLogSource) Subscribe(n int) ([]string, <-chan string, func()) {
	return f.tail, f.ch, func() {
		f.mu.Lock()
		f.cancelled = true
		f.mu.Unlock()
	}
}

func (f *fakeLogSource) wasCancelled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelled
}

func logsRouter(t *testing.T, src LogSource) (http.Handler, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "obsboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	opts := auth.Options{PublicURL: dashOrigin, Admins: []string{"alice@percona.com"}, Provider: auth.NewProvider(oauth2.Config{}, nil)}
	sid, err := auth.NewStore(db).CreateSession("alice@percona.com", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	obsClient := obs.SingleFleet(obs.NewClient(stubOBSServer(t).URL, "user", "pass"), "isv:percona")
	r := NewRouter(db, hub.New(), obsClient, fakeStatter{}, new(atomic.Bool), time.Duration(0), fakePollState{s: "active"}, auth.NewService(db, opts), nil, nil, nil, src)
	return r, sid
}

// syncBuf is a ResponseWriter whose body can be read while the handler runs.
type syncBuf struct {
	httptest.ResponseRecorder
	mu sync.Mutex
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ResponseRecorder.Write(p)
}

func (b *syncBuf) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Body.String()
}

// startLogStream serves GET /api/logs/stream as sid and returns the live
// response, a cancel for the request, and a channel closed when the handler
// returns.
func startLogStream(t *testing.T, h http.Handler, sid string) (*syncBuf, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req := httptest.NewRequest(http.MethodGet, "/api/logs/stream", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: "obsboard_session", Value: sid})
	rec := &syncBuf{ResponseRecorder: *httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()
	return rec, cancel, done
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLogStreamRequiresUser(t *testing.T) {
	h, _ := logsRouter(t, newFakeLogSource())
	if rec := callRec(h, http.MethodGet, "/api/logs/stream", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon: %d", rec.Code)
	}
}

func TestLogStreamSendsResetThenLines(t *testing.T) {
	src := newFakeLogSource("a", "b")
	h, sid := logsRouter(t, src)
	rec, cancel, done := startLogStream(t, h, sid)

	src.ch <- `c "quoted"`
	want := "event: reset\ndata: [\"a\",\"b\"]\n\nevent: line\ndata: \"c \\\"quoted\\\"\"\n\n"
	waitFor(t, "reset and line events", func() bool { return strings.Contains(rec.text(), want) })
	cancel()
	<-done
}

func TestLogStreamEmptyTail(t *testing.T) {
	src := newFakeLogSource()
	h, sid := logsRouter(t, src)
	rec, cancel, done := startLogStream(t, h, sid)

	waitFor(t, "reset event", func() bool { return strings.Contains(rec.text(), "event: reset\ndata: []\n\n") })
	cancel()
	<-done
}

func TestLogStreamCancelsOnDisconnect(t *testing.T) {
	src := newFakeLogSource("a")
	h, sid := logsRouter(t, src)
	rec, cancel, done := startLogStream(t, h, sid)

	waitFor(t, "reset event", func() bool { return strings.Contains(rec.text(), "event: reset") })
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return after disconnect")
	}
	if !src.wasCancelled() {
		t.Fatal("subscription was not cancelled")
	}
}

func TestLogStreamEndsWhenFeedCloses(t *testing.T) {
	src := newFakeLogSource()
	h, sid := logsRouter(t, src)
	_, _, done := startLogStream(t, h, sid)

	close(src.ch)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler kept running on a closed feed")
	}
}

func TestLogStreamAbsentWithoutSource(t *testing.T) {
	h, sid := logsRouter(t, nil)
	if rec := callRec(h, http.MethodGet, "/api/logs/stream", "", sid); rec.Code != http.StatusNotFound {
		t.Fatalf("no source: %d", rec.Code)
	}
}
