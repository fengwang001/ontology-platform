package semver

import "strings"

// Version is a parsed SemVer 2.0.0 version.
type Version struct {
	Major      uint64
	Minor      uint64
	Patch      uint64
	Pre        []string
	Build      []string
	raw        string
}

// String restores the original input verbatim.
func (v Version) String() string { return v.raw }

// IsPrerelease reports whether the version carries a prerelease tag.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// Parse parses a SemVer 2.0.0 version string.
func Parse(s string) (Version, error) {
	return Version{}, nil
}

func isIdentChar(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b == '-'
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) < 0
}
