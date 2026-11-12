package main

import (
	"bytes"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/percona/obs-dashboard/internal/logsink"
)

// slog output keeps the standard logger's shape and lands in the file once
// the sink is the logger's output.
func TestSlogLandsInLogFileInStandardShape(t *testing.T) {
	var stderr bytes.Buffer
	sink := logsink.New(&stderr)
	prev := log.Writer()
	log.SetOutput(sink)
	t.Cleanup(func() { log.SetOutput(prev) })

	path := filepath.Join(t.TempDir(), "obs-dashboard.log")
	sink.Open(path)
	slog.Info("hello", "k", "v")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} INFO hello k=v\n$`)
	if !re.Match(data) {
		t.Fatalf("file = %q", data)
	}
	if !bytes.Equal(data, stderr.Bytes()) {
		t.Fatalf("stderr %q differs from file %q", stderr.Bytes(), data)
	}
}
