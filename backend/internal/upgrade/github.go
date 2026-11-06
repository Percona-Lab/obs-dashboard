package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Release is one published release as the dashboard sees it.
type Release struct {
	Version     string
	Notes       string
	URL         string
	PublishedAt time.Time
	HostChanges string
}

// ReleaseSource lists published releases.
type ReleaseSource interface {
	Releases(ctx context.Context) ([]Release, error)
}

// GitHubSource reads releases anonymously from the GitHub REST API.
type GitHubSource struct {
	APIBase string // default https://api.github.com
	Repo    string // owner/name
	Client  *http.Client
}

// Releases returns published, non-prerelease vX.Y.Z releases.
func (g GitHubSource) Releases(ctx context.Context) ([]Release, error) {
	base := g.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+g.Repo+"/releases?per_page=30", nil)
	if err != nil {
		return nil, fmt.Errorf("github releases: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: HTTP %d", resp.StatusCode)
	}
	var raw []struct {
		TagName     string     `json:"tag_name"`
		Body        string     `json:"body"`
		HTMLURL     string     `json:"html_url"`
		Draft       bool       `json:"draft"`
		Prerelease  bool       `json:"prerelease"`
		PublishedAt *time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("github releases: decode: %w", err)
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		if r.Draft || r.Prerelease || r.PublishedAt == nil || !ValidVersion(r.TagName) {
			continue
		}
		out = append(out, Release{
			Version:     r.TagName,
			Notes:       r.Body,
			URL:         r.HTMLURL,
			PublishedAt: r.PublishedAt.UTC(),
			HostChanges: HostChanges(r.Body),
		})
	}
	return out, nil
}

var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)

// HostChanges returns the body of the "Host changes" subsection (heading
// level 2-4, case-insensitive) up to the next heading of the same or a
// higher level, trimmed. Empty when absent.
func HostChanges(notes string) string {
	lines := strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n")
	level := 0
	var out []string
	for _, l := range lines {
		m := headingRe.FindStringSubmatch(l)
		if level == 0 {
			if m != nil && len(m[1]) >= 2 && len(m[1]) <= 4 && strings.EqualFold(m[2], "Host changes") {
				level = len(m[1])
			}
			continue
		}
		if m != nil && len(m[1]) <= level {
			break
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
