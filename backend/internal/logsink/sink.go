// Package logsink is an io.Writer for the standard logger. It copies every
// write to stderr and to a size-capped log file, and lets readers take the
// last lines plus a live feed of the lines that follow.
package logsink

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const (
	earlyLines   = 1000
	subscriberCh = 256
	readChunk    = 64 << 10
)

// Sink is safe for concurrent use. One mutex guards the file, its size, the
// early buffer and the subscriber set, and Write does all its work under it,
// so a Subscribe taken under the same lock sees no gap and no duplicate.
type Sink struct {
	mu      sync.Mutex
	stderr  io.Writer
	maxSize int64

	path   string // set by Open; empty until then
	file   *os.File
	size   int64
	early  []string // lines written before Open, capped at earlyLines
	subs   map[chan string]struct{}
	failed bool // a file error was already reported
}

// New returns a sink that copies every write to stderr. Until Open is
// called it keeps the last 1000 lines in memory.
func New(stderr io.Writer) *Sink {
	return &Sink{
		stderr:  stderr,
		maxSize: 10 << 20,
		subs:    make(map[chan string]struct{}),
	}
}

// Write implements io.Writer. It always returns len(p), nil.
func (s *Sink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, _ = s.stderr.Write(p)

	lines := splitLines(p)
	if len(lines) == 0 {
		return len(p), nil
	}

	if s.path == "" {
		s.early = append(s.early, lines...)
		if over := len(s.early) - earlyLines; over > 0 {
			s.early = append(s.early[:0], s.early[over:]...)
		}
	} else {
		s.writeFile(lines)
	}

	for _, l := range lines {
		for ch := range s.subs {
			select {
			case ch <- l:
			default: // a slow viewer loses lines; Write never waits
			}
		}
	}
	return len(p), nil
}

// Open starts writing to path (append, create, 0o640), first writing the
// buffered early lines. On failure it writes one line to stderr and the
// sink carries on without a file.
func (s *Sink) Open(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	early := s.early
	s.early = nil
	s.path = path

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err == nil {
		var fi os.FileInfo
		if fi, err = f.Stat(); err != nil {
			_ = f.Close()
		} else {
			s.file, s.size = f, fi.Size()
		}
	}
	if err != nil {
		s.fileFailed(err)
		return
	}
	if len(early) > 0 {
		s.writeFile(early)
	}
}

// Subscribe returns the last n lines and a channel of the lines written
// after them, with no gap and no duplicate. cancel removes the
// subscription and is safe to call more than once.
func (s *Sink) Subscribe(n int) (tail []string, lines <-chan string, cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.path == "" {
		tail = lastN(s.early, n)
	} else {
		tail = tailFiles(s.path, n)
	}

	ch := make(chan string, subscriberCh)
	s.subs[ch] = struct{}{}
	cancel = func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
	}
	return tail, ch, cancel
}

// writeFile appends lines to the current file and rotates it once it is
// over maxSize. Callers hold s.mu.
func (s *Sink) writeFile(lines []string) {
	if s.file == nil {
		return
	}
	data := strings.Join(lines, "\n") + "\n"
	n, err := s.file.WriteString(data)
	s.size += int64(n)
	if err != nil {
		s.fileFailed(err)
		return
	}
	if s.size > s.maxSize {
		s.rotate()
	}
}

func (s *Sink) rotate() {
	_ = s.file.Close()
	s.file = nil
	if err := os.Rename(s.path, s.path+".1"); err != nil {
		s.fileFailed(err)
		return
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		s.fileFailed(err)
		return
	}
	s.file, s.size = f, 0
}

// fileFailed reports a file error once on stderr and stops using the file.
func (s *Sink) fileFailed(err error) {
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	if s.failed {
		return
	}
	s.failed = true
	fmt.Fprintf(s.stderr, "logsink: cannot use log file %s: %v\n", s.path, err)
}

// splitLines splits p into entries without their trailing newline.
func splitLines(p []byte) []string {
	if len(p) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(p), "\n"), "\n")
}

func lastN(lines []string, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return append([]string(nil), lines...)
}

// tailFiles returns the last n lines across path.1 and path, oldest first.
func tailFiles(path string, n int) []string {
	if n <= 0 {
		return nil
	}
	cur := tailFile(path, n)
	if len(cur) < n {
		cur = append(tailFile(path+".1", n-len(cur)), cur...)
	}
	return cur
}

// tailFile returns the last n lines of the file at path, reading backwards in
// chunks until it has seen enough newlines. A missing file has no lines.
func tailFile(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}

	var buf []byte
	pos := fi.Size()
	for pos > 0 && bytes.Count(buf, []byte{'\n'}) <= n {
		step := int64(readChunk)
		if step > pos {
			step = pos
		}
		pos -= step
		chunk := make([]byte, step)
		if _, err := f.ReadAt(chunk, pos); err != nil {
			return nil
		}
		buf = append(chunk, buf...)
	}

	lines := splitLines(buf)
	if pos > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line may be cut off
	}
	return lastN(lines, n)
}
