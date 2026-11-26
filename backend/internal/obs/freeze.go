package obs

import (
	"context"
	"encoding/xml"
	"io"
	"net/url"
)

// InstanceFreeze is the freeze state of one release unit on one OBS instance.
type InstanceFreeze struct {
	Instance string // Instance.Name
	State    string // "frozen" | "partial" | "unfrozen" | "unknown"
}

// parseFreezeState classifies a project _meta document by its repositories:
// rebuild="local" on all of them is frozen, on none unfrozen, on some partial,
// and a project without repositories is empty.
func parseFreezeState(metaXML []byte) (string, error) {
	var doc struct {
		Repos []struct {
			Rebuild string `xml:"rebuild,attr"`
		} `xml:"repository"`
	}
	if err := xml.Unmarshal(metaXML, &doc); err != nil {
		return "", err
	}
	local := 0
	for _, r := range doc.Repos {
		if r.Rebuild == "local" {
			local++
		}
	}
	switch {
	case len(doc.Repos) == 0:
		return "empty", nil
	case local == len(doc.Repos):
		return "frozen", nil
	case local == 0:
		return "unfrozen", nil
	}
	return "partial", nil
}

// ProjectFreezeState reads a project's _meta and returns its freeze state.
// Not cached: freezing changes it at any time.
func (c *Client) ProjectFreezeState(ctx context.Context, project string) (string, error) {
	resp, err := c.get(ctx, "freeze_state", "/source/"+url.PathEscape(project)+"/_meta")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return parseFreezeState(body)
}

// UnitFreeze returns the freeze state of a release unit on each instance that
// hosts it, in fleet order. Projects answering 404 or empty do not vote, and an
// instance with no voting project is left out. Any other read error makes that
// instance "unknown".
func (f *Fleet) UnitFreeze(ctx context.Context, unit string) []InstanceFreeze {
	res := fanOut(f.Instances(), func(in *Instance) (string, error) {
		votes := 0
		frozen := 0
		unfrozen := 0
		for _, logical := range []string{unit, unit + ":containers", unit + ":tarballs"} {
			st, err := in.Client.ProjectFreezeState(ctx, in.ToInstance(logical))
			if err != nil {
				if IsNotFound(err) {
					continue
				}
				return "unknown", nil
			}
			switch st {
			case "empty":
			case "frozen":
				votes++
				frozen++
			case "unfrozen":
				votes++
				unfrozen++
			default:
				votes++
			}
		}
		switch {
		case votes == 0:
			return "", nil
		case frozen == votes:
			return "frozen", nil
		case unfrozen == votes:
			return "unfrozen", nil
		}
		return "partial", nil
	})
	var out []InstanceFreeze
	for _, r := range res {
		if r.val != "" {
			out = append(out, InstanceFreeze{Instance: r.inst.Name, State: r.val})
		}
	}
	return out
}
