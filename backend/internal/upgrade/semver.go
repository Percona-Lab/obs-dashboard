// Package upgrade lists newer releases and hands approved upgrades to the
// host through the apply-request/apply-status file contract.
package upgrade

import (
	"regexp"
	"strconv"
)

var versionRe = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

type semver [3]int

func parseVersion(s string) (semver, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return semver{}, false
		}
		v[i] = n
	}
	return v, true
}

// ValidVersion reports whether s is a plain vX.Y.Z tag.
func ValidVersion(s string) bool {
	_, ok := parseVersion(s)
	return ok
}

// Newer reports whether a is a strictly higher vX.Y.Z than b. Invalid input
// (including "dev") is never newer and nothing is newer than it.
func Newer(a, b string) bool {
	va, ok1 := parseVersion(a)
	vb, ok2 := parseVersion(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}
