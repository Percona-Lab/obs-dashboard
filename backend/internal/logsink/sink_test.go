package logsink

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func newOpened(t *testing.T, maxSize int64) (*Sink, *bytes.Buffer, string) {
	t.Helper()
	var stderr bytes.Buffer
	s := New(&stderr)
	if maxSize > 0 {
		s.maxSize = maxSize
	}
	path := filepath.Join(t.TempDir(), "obs-dashboard.log")
	s.Open(path)
	return s, &stderr, path
}

func mustWrite(t *testing.T, s *Sink, p string) {
	t.Helper()
	n, err := s.Write([]byte(p))
	if n != len(p) || err != nil {
		t.Fatalf("Write(%d bytes) = %d, %v", len(p), n, err)
	}
}

func recv(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case l := <-ch:
		return l
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a line")
		return ""
	}
}

func TestWriteReachesStderrFileAndSubscriber(t *testing.T) {
	s, stderr, path := newOpened(t, 0)
	_, ch, cancel := s.Subscribe(0)
	defer cancel()
	mustWrite(t, s, "a\nb\n")
	if stderr.String() != "a\nb\n" {
		t.Errorf("stderr = %q", stderr.String())
	}
	if got := readFile(t, path); got != "a\nb\n" {
		t.Errorf("file = %q", got)
	}
	if a, b := recv(t, ch), recv(t, ch); a != "a" || b != "b" {
		t.Errorf("channel yielded %q, %q", a, b)
	}
}

func TestEarlyLinesAreBufferedThenWritten(t *testing.T) {
	var stderr bytes.Buffer
	s := New(&stderr)
	for _, l := range []string{"one\n", "two\n", "three\n"} {
		mustWrite(t, s, l)
	}
	tail, _, cancel := s.Subscribe(10)
	cancel()
	if strings.Join(tail, ",") != "one,two,three" {
		t.Errorf("tail = %v", tail)
	}
	path := filepath.Join(t.TempDir(), "obs-dashboard.log")
	s.Open(path)
	if got := readFile(t, path); got != "one\ntwo\nthree\n" {
		t.Errorf("file = %q", got)
	}
	tail, _, cancel = s.Subscribe(10)
	cancel()
	if strings.Join(tail, ",") != "one,two,three" {
		t.Errorf("tail after Open = %v", tail)
	}
}

func TestEarlyBufferIsCapped(t *testing.T) {
	var stderr bytes.Buffer
	s := New(&stderr)
	for i := 1; i <= 1005; i++ {
		mustWrite(t, s, fmt.Sprintf("line %d\n", i))
	}
	tail, _, cancel := s.Subscribe(2000)
	cancel()
	if len(tail) != 1000 || tail[0] != "line 6" || tail[999] != "line 1005" {
		t.Errorf("len=%d first=%q last=%q", len(tail), tail[0], tail[len(tail)-1])
	}
}

func TestRotationKeepsTwoFiles(t *testing.T) {
	s, _, path := newOpened(t, 64)
	for i := 0; i < 40; i++ {
		mustWrite(t, s, fmt.Sprintf("line %04d\n", i)) // 10 bytes
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "obs-dashboard.log,obs-dashboard.log.1" {
		t.Errorf("dir = %v", names)
	}
	if fi, _ := os.Stat(path); fi.Size() > 64+10 {
		t.Errorf("current file is %d bytes", fi.Size())
	}
}

func TestTailSpansBothFiles(t *testing.T) {
	s, _, path := newOpened(t, 64)
	for i := 0; i < 20; i++ {
		mustWrite(t, s, fmt.Sprintf("line %04d\n", i))
	}
	cur := strings.Count(readFile(t, path), "\n")
	prev := strings.Count(readFile(t, path+".1"), "\n")
	if prev == 0 || cur >= 10 {
		t.Fatalf("setup: cur=%d prev=%d", cur, prev)
	}
	n := 10
	tail, _, cancel := s.Subscribe(n)
	cancel()
	if len(tail) != n {
		t.Fatalf("tail has %d lines: %v", len(tail), tail)
	}
	for i, l := range tail {
		if want := fmt.Sprintf("line %04d", 20-n+i); l != want {
			t.Errorf("tail[%d] = %q, want %q", i, l, want)
		}
	}
}

func TestSubscribeHasNoGapOrDuplicate(t *testing.T) {
	s, _, _ := newOpened(t, 0)
	// At most 200 lines are written after Subscribe, fewer than the 256 a
	// subscriber's channel holds, so no line is dropped however slowly the
	// reader below runs.
	const total = 700
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < total; i++ {
			if i == 500 {
				close(started)
			}
			mustWrite(t, s, fmt.Sprintf("%d\n", i))
		}
	}()
	<-started
	tail, ch, cancel := s.Subscribe(5000)
	var got []string
	got = append(got, tail...)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for l := range ch {
			got = append(got, l)
		}
	}()
	<-done
	cancel() // closes ch once the writer is finished
	<-drained
	if len(got) == 0 || got[len(got)-1] != fmt.Sprint(total-1) {
		t.Fatalf("last line is not %d (len %d)", total-1, len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i] != fmt.Sprint(atoi(got[i-1])+1) {
			t.Fatalf("not contiguous at %d: %q then %q", i, got[i-1], got[i])
		}
	}
}

func atoi(s string) int {
	var n int
	fmt.Sscan(s, &n)
	return n
}

func TestFullSubscriberDoesNotBlock(t *testing.T) {
	s, stderr, _ := newOpened(t, 0)
	_, _, cancel := s.Subscribe(0)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			mustWrite(t, s, fmt.Sprintf("line %d\n", i))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write blocked on a full subscriber")
	}
	if n := strings.Count(stderr.String(), "\n"); n != 1000 {
		t.Errorf("stderr has %d lines", n)
	}
}

func TestCancelRemovesSubscriber(t *testing.T) {
	s, _, _ := newOpened(t, 0)
	_, _, cancel := s.Subscribe(0)
	cancel()
	cancel()
	mustWrite(t, s, "x\n")
}

func TestUnwritableFileDoesNotFailWrites(t *testing.T) {
	var stderr bytes.Buffer
	s := New(&stderr)
	mustWrite(t, s, "early\n")
	stderr.Reset()
	bad := filepath.Join(t.TempDir(), "missing", "x.log")
	s.Open(bad)
	if n := strings.Count(stderr.String(), "\n"); n != 1 || !strings.Contains(stderr.String(), bad) {
		t.Errorf("stderr after Open = %q", stderr.String())
	}
	tail, ch, cancel := s.Subscribe(10)
	defer cancel()
	if len(tail) != 0 {
		t.Errorf("tail = %v", tail)
	}
	mustWrite(t, s, "later\n")
	if l := recv(t, ch); l != "later" {
		t.Errorf("got %q", l)
	}
	if !strings.HasSuffix(stderr.String(), "later\n") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestSubscriberSurvivesRotation(t *testing.T) {
	s, _, _ := newOpened(t, 64)
	_, ch, cancel := s.Subscribe(0)
	defer cancel()
	const total = 100 // 3+ rotations at 10 bytes per line
	for i := 0; i < total; i++ {
		mustWrite(t, s, fmt.Sprintf("line %04d\n", i))
		if got, want := recv(t, ch), fmt.Sprintf("line %04d", i); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestConcurrentWritersKeepLinesWhole(t *testing.T) {
	s, _, path := newOpened(t, 0)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				mustWrite(t, s, fmt.Sprintf("g%d-%d\n", i, j))
			}
		}()
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSuffix(readFile(t, path), "\n"), "\n")
	if len(lines) != 1600 {
		t.Fatalf("file has %d lines", len(lines))
	}
	re := regexp.MustCompile(`^g\d-\d+$`)
	for _, l := range lines {
		if !re.MatchString(l) {
			t.Fatalf("torn line %q", l)
		}
	}
}

func TestVeryLongLine(t *testing.T) {
	s, _, _ := newOpened(t, 0)
	long := strings.Repeat("x", 64<<10)
	mustWrite(t, s, long+"\n")
	mustWrite(t, s, "end\n")
	tail, _, cancel := s.Subscribe(2)
	cancel()
	if len(tail) != 2 || tail[0] != long || tail[1] != "end" {
		t.Errorf("tail has %d entries", len(tail))
	}
}
