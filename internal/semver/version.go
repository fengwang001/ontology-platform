package semver

import (
	"strconv"
	"strings"
)

// Version is a parsed SemVer 2.0.0 version.
type Version struct {
	Major uint64
	Minor uint64
	Patch uint64
	Pre   []string
	Build []string
	raw   string
}

// String restores the original input verbatim.
func (v Version) String() string { return v.raw }

// IsPrerelease reports whether the version carries a prerelease tag.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

// Parse parses a SemVer 2.0.0 version string.
func Parse(s string) (Version, error) {
	rest := s
	var build, pre string
	hasPre := false
	if i := strings.IndexByte(rest, '+'); i >= 0 {
		build = rest[i+1:]
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		pre = rest[i+1:]
		rest = rest[:i]
		hasPre = true
	}
	core := strings.Split(rest, ".")
	if len(core) != 3 {
		return Version{}, &ParseError{Kind: "version", Input: s, Err: ErrInvalidVersion}
	}
	nums := make([]uint64, 3)
	for i, part := range core {
		n, err := parseNumeric(part)
		if err != nil {
			return Version{}, &ParseError{Kind: "version", Input: s, Err: err}
		}
		nums[i] = n
	}
	v := Version{Major: nums[0], Minor: nums[1], Patch: nums[2], raw: s}
	if hasPre {
		ids, err := parseIdents(pre, false)
		if err != nil {
			return Version{}, &ParseError{Kind: "version", Input: s, Err: err}
		}
		v.Pre = ids
	}
	if build != "" || strings.Contains(s, "+") {
		ids, err := parseIdents(build, true)
		if err != nil {
			return Version{}, &ParseError{Kind: "version", Input: s, Err: err}
		}
		v.Build = ids
	}
	return v, nil
}

// parseNumeric parses a decimal number that must not have leading zeros.
func parseNumeric(s string) (uint64, error) {
	if !isDigits(s) {
		return 0, ErrInvalidVersion
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, ErrInvalidVersion
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, ErrInvalidVersion
	}
	return n, nil
}

// parseIdents validates a dot-separated identifier list. Numeric identifiers
// must not have leading zeros unless allowLeadingZero (build metadata).
func parseIdents(s string, allowLeadingZero bool) ([]string, error) {
	ids := strings.Split(s, ".")
	for _, id := range ids {
		if id == "" {
			return nil, ErrInvalidVersion
		}
		for i := 0; i < len(id); i++ {
			if !isIdentChar(id[i]) {
				return nil, ErrInvalidVersion
			}
		}
		if !allowLeadingZero && isDigits(id) && len(id) > 1 && id[0] == '0' {
			return nil, ErrInvalidVersion
		}
	}
	return ids, nil
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
