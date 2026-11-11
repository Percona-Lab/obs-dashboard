package release

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

var (
	ErrSectionNotFound  = errors.New("changelog section not found")
	ErrSectionAmbiguous = errors.New("changelog section is ambiguous")
	ErrBadSection       = errors.New("invalid changelog section")
)

const sectionPrefix = "## ["

// Record is the current entry of a release.yaml.
type Record struct {
	Project      string
	Tag          string
	SourceCommit string
}

// ExtractSection returns the changelog section for id: from its "## [id]"
// heading up to, not including, the next "## [" heading or end of file.
func ExtractSection(changelog []byte, id string) (string, error) {
	lines := splitLines(changelog)
	start, end, err := findSection(lines, id)
	if err != nil {
		return "", err
	}
	return strings.Join(lines[start:end], ""), nil
}

// ReplaceSection replaces the changelog section for id with section. Bytes
// outside the section are kept as they are. The replacement must start with
// its own "## [id]" heading and contain no other "## [" heading. Line endings
// follow the changelog: a CRLF file stays CRLF.
func ReplaceSection(changelog []byte, id, section string) ([]byte, error) {
	lines := splitLines(changelog)
	start, end, err := findSection(lines, id)
	if err != nil {
		return nil, err
	}

	crlf := bytes.Contains(changelog, []byte("\r\n"))
	section = strings.ReplaceAll(section, "\r\n", "\n")
	repl := splitLines([]byte(section))
	if len(repl) == 0 || headingID(repl[0]) != id {
		return nil, fmt.Errorf("%w: must start with %s%s]", ErrBadSection, sectionPrefix, id)
	}
	for _, l := range repl[1:] {
		if strings.HasPrefix(l, sectionPrefix) {
			return nil, fmt.Errorf("%w: contains another changelog heading", ErrBadSection)
		}
	}

	// The replaced section ends the file without a line terminator. Keep it
	// that way when the replacement also has none, so the bytes stay identical.
	bareEOF := end == len(lines) && !strings.HasSuffix(lines[end-1], "\n")

	var out bytes.Buffer
	for _, l := range lines[:start] {
		out.WriteString(l)
	}
	for i, l := range repl {
		body := strings.TrimSuffix(l, "\n")
		out.WriteString(body)
		if i == len(repl)-1 && bareEOF && !strings.HasSuffix(l, "\n") {
			break
		}
		if crlf {
			out.WriteString("\r\n")
		} else {
			out.WriteString("\n")
		}
	}
	for _, l := range lines[end:] {
		out.WriteString(l)
	}
	return out.Bytes(), nil
}

// CurrentRelease returns the last entry of the releases list in release.yaml
// and the project value. An entry is either a plain tag string or a mapping
// with tag and source_commit.
func CurrentRelease(releaseYAML []byte) (Record, error) {
	var doc struct {
		Project  string      `yaml:"project"`
		Releases []yaml.Node `yaml:"releases"`
	}
	if err := yaml.Unmarshal(releaseYAML, &doc); err != nil {
		return Record{}, fmt.Errorf("parse release.yaml: %w", err)
	}
	if len(doc.Releases) == 0 {
		return Record{}, errors.New("release.yaml has no releases")
	}
	last := doc.Releases[len(doc.Releases)-1]
	rec := Record{Project: doc.Project}
	if last.Kind == yaml.ScalarNode {
		rec.Tag = last.Value
		return rec, nil
	}
	var entry struct {
		Tag          string `yaml:"tag"`
		SourceCommit string `yaml:"source_commit"`
	}
	if err := last.Decode(&entry); err != nil {
		return Record{}, fmt.Errorf("parse release entry: %w", err)
	}
	rec.Tag = entry.Tag
	rec.SourceCommit = entry.SourceCommit
	return rec, nil
}

// ReleaseID returns the release id of a tag: the text after the first "/",
// so "ppg/18.6-6" becomes "18.6-6". A tag without "/" is returned unchanged.
func ReleaseID(tag string) string {
	if _, after, found := strings.Cut(tag, "/"); found {
		return after
	}
	return tag
}

// splitLines splits data after each "\n", keeping the terminators.
func splitLines(data []byte) []string {
	s := string(data)
	var lines []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}

// headingID returns the id of a "## [id]" heading line, or "" if the line is
// not one.
func headingID(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, sectionPrefix) {
		return ""
	}
	rest := line[len(sectionPrefix):]
	i := strings.IndexByte(rest, ']')
	if i < 0 {
		return ""
	}
	return rest[:i]
}

// findSection returns the line index range [start, end) of the section for id.
func findSection(lines []string, id string) (int, int, error) {
	start, found := -1, 0
	for i, l := range lines {
		if headingID(l) == id && id != "" {
			found++
			if start < 0 {
				start = i
			}
		}
	}
	if found == 0 {
		return 0, 0, ErrSectionNotFound
	}
	if found > 1 {
		return 0, 0, ErrSectionAmbiguous
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], sectionPrefix) {
			end = i
			break
		}
	}
	return start, end, nil
}
