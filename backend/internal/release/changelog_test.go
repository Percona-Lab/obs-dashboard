package release

import (
	"errors"
	"strings"
	"testing"
)

const testChangelog = "# Changelog\n\n## [18.6-6] - 2026-11-11\n\n### Changed\n- a\n\n## [18.6-5] - 2026-10-01\n\n### Changed\n- b\n"

func TestExtractSection(t *testing.T) {
	got, err := ExtractSection([]byte(testChangelog), "18.6-6")
	if err != nil {
		t.Fatalf("ExtractSection: %v", err)
	}
	want := "## [18.6-6] - 2026-11-11\n\n### Changed\n- a\n\n"
	if got != want {
		t.Errorf("ExtractSection = %q, want %q", got, want)
	}
}

func TestExtractSectionLastRunsToEOF(t *testing.T) {
	got, err := ExtractSection([]byte(testChangelog), "18.6-5")
	if err != nil {
		t.Fatalf("ExtractSection: %v", err)
	}
	want := "## [18.6-5] - 2026-10-01\n\n### Changed\n- b\n"
	if got != want {
		t.Errorf("ExtractSection = %q, want %q", got, want)
	}
}

func TestReplaceSectionKeepsRest(t *testing.T) {
	out, err := ReplaceSection([]byte(testChangelog), "18.6-6", "## [18.6-6] - 2026-11-11\n\n### Changed\n- a\n- c\n\n")
	if err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "# Changelog\n\n") {
		t.Errorf("prefix changed: %q", s)
	}
	if !strings.HasSuffix(s, "## [18.6-5] - 2026-10-01\n\n### Changed\n- b\n") {
		t.Errorf("suffix changed: %q", s)
	}
	if !strings.Contains(s, "- c") {
		t.Errorf("replacement missing: %q", s)
	}
}

func TestReplaceSectionCRLF(t *testing.T) {
	in := strings.ReplaceAll(testChangelog, "\n", "\r\n")
	out, err := ReplaceSection([]byte(in), "18.6-6", "## [18.6-6] - 2026-11-11\r\n\r\n### Changed\r\n- c\r\n\r\n")
	if err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}
	s := string(out)
	if strings.Count(s, "\n") != strings.Count(s, "\r\n") {
		t.Errorf("bare LF in output: %q", s)
	}
	if !strings.Contains(s, "- c\r\n") {
		t.Errorf("replacement missing: %q", s)
	}
}

func TestSectionMissing(t *testing.T) {
	if _, err := ExtractSection([]byte(testChangelog), "9.9-9"); !errors.Is(err, ErrSectionNotFound) {
		t.Errorf("ExtractSection err = %v, want ErrSectionNotFound", err)
	}
	if _, err := ReplaceSection([]byte(testChangelog), "9.9-9", "## [9.9-9]\n"); !errors.Is(err, ErrSectionNotFound) {
		t.Errorf("ReplaceSection err = %v, want ErrSectionNotFound", err)
	}
}

func TestSectionAmbiguous(t *testing.T) {
	dup := testChangelog + "\n## [18.6-6] - 2026-11-12\n\n- d\n"
	if _, err := ExtractSection([]byte(dup), "18.6-6"); !errors.Is(err, ErrSectionAmbiguous) {
		t.Errorf("ExtractSection err = %v, want ErrSectionAmbiguous", err)
	}
	if _, err := ReplaceSection([]byte(dup), "18.6-6", "## [18.6-6]\n"); !errors.Is(err, ErrSectionAmbiguous) {
		t.Errorf("ReplaceSection err = %v, want ErrSectionAmbiguous", err)
	}
}

func TestReplaceRejectsForeignHeading(t *testing.T) {
	cases := map[string]string{
		"foreign heading":    "## [18.6-6] - 2026-11-11\n\n## [18.6-5] - 2026-10-01\n",
		"wrong leading id":   "## [18.6-5] - 2026-10-01\n\n- x\n",
		"no leading heading": "- x\n",
		"empty replacement":  "",
	}
	for name, section := range cases {
		if _, err := ReplaceSection([]byte(testChangelog), "18.6-6", section); !errors.Is(err, ErrBadSection) {
			t.Errorf("%s: err = %v, want ErrBadSection", name, err)
		}
	}
}

func TestSectionIDIsExact(t *testing.T) {
	cl := "## [18.6-60] - 2026-11-11\n\n- x\n"
	if _, err := ExtractSection([]byte(cl), "18.6-6"); !errors.Is(err, ErrSectionNotFound) {
		t.Errorf("ExtractSection err = %v, want ErrSectionNotFound", err)
	}
}

func TestCurrentRelease(t *testing.T) {
	y := "repository: x\nproject: ppg:staging:18\nreleases:\n  - ppg/18.6-5\n  - tag: ppg/18.6-6\n    source_commit: abc\n"
	rec, err := CurrentRelease([]byte(y))
	if err != nil {
		t.Fatalf("CurrentRelease: %v", err)
	}
	want := Record{Project: "ppg:staging:18", Tag: "ppg/18.6-6", SourceCommit: "abc"}
	if rec != want {
		t.Errorf("CurrentRelease = %+v, want %+v", rec, want)
	}
	if id := ReleaseID(rec.Tag); id != "18.6-6" {
		t.Errorf("ReleaseID = %q", id)
	}
}

func TestCurrentReleasePlainString(t *testing.T) {
	y := "project: ppg:staging:18\nreleases:\n  - tag: ppg/18.6-5\n    source_commit: abc\n  - ppg/18.6-6\n"
	rec, err := CurrentRelease([]byte(y))
	if err != nil {
		t.Fatalf("CurrentRelease: %v", err)
	}
	want := Record{Project: "ppg:staging:18", Tag: "ppg/18.6-6"}
	if rec != want {
		t.Errorf("CurrentRelease = %+v, want %+v", rec, want)
	}
}

func TestReleaseID(t *testing.T) {
	if got := ReleaseID("ppg/18.6-6"); got != "18.6-6" {
		t.Errorf("ReleaseID = %q", got)
	}
}
