package update

import (
	"strconv"
	"strings"
)

// semver is a minimal major.minor.patch version with an optional prerelease
// suffix. wisp tags are simple (v1.2.3, v1.2.3-rc1), so a full SemVer 2.0.0
// implementation is unnecessary; this covers the ordering rules that matter:
// numeric precedence on the three core fields, and a prerelease being lower
// than its corresponding release.
type semver struct {
	major, minor, patch int
	pre                 string
	ok                  bool // parsed successfully
}

func parseSemver(s string) semver {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return semver{}
	}
	core := s
	pre := ""
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		core = s[:i]
		if s[i] == '-' {
			pre = s[i+1:]
			if j := strings.IndexByte(pre, '+'); j >= 0 {
				pre = pre[:j] // drop build metadata
			}
		}
	}
	parts := strings.Split(core, ".")
	v := semver{pre: pre, ok: true}
	get := func(i int) int {
		if i >= len(parts) {
			return 0
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			v.ok = false
			return 0
		}
		return n
	}
	v.major = get(0)
	v.minor = get(1)
	v.patch = get(2)
	return v
}

// compare returns -1 if a < b, 0 if equal, +1 if a > b.
func (a semver) compare(b semver) int {
	for _, d := range []struct{ x, y int }{
		{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch},
	} {
		if d.x != d.y {
			if d.x < d.y {
				return -1
			}
			return 1
		}
	}
	// Equal core. A prerelease is lower precedence than the release.
	switch {
	case a.pre == "" && b.pre == "":
		return 0
	case a.pre == "" && b.pre != "":
		return 1 // release > prerelease
	case a.pre != "" && b.pre == "":
		return -1
	default:
		return strings.Compare(a.pre, b.pre)
	}
}

// IsNewer reports whether candidate is a strictly newer version than current.
// A "dev"/empty current is always considered older, so a local build always
// sees a real release as an update. An unparseable candidate is never newer.
func IsNewer(current, candidate string) bool {
	cand := parseSemver(candidate)
	if !cand.ok {
		return false
	}
	cur := parseSemver(current)
	if !cur.ok {
		// current is "dev" or malformed: any valid release is newer.
		return true
	}
	return cur.compare(cand) < 0
}
