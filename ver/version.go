// Package ver parses and compares semantic versions (major.minor.patch[-pre]).
package ver

import (
	"errors"
	"strconv"
	"strings"
)

// ErrSyntax is returned for a malformed version string.
var ErrSyntax = errors.New("ver: invalid version syntax")

// Version is a parsed major.minor.patch[-prerelease] version.
type Version struct {
	raw    string
	nums   [3]uint64
	pre    []string // nil means release (greater than any prerelease)
	hasPre bool
}

// Parse parses forms like "1.2.3" and "1.2.3-rc.1".
func Parse(s string) (Version, error) {
	raw := s
	var pre []string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		prePart := s[i+1:]
		s = s[:i]
		if prePart == "" || strings.HasPrefix(prePart, ".") || strings.HasSuffix(prePart, ".") {
			return Version{}, ErrSyntax
		}
		pre = strings.Split(prePart, ".")
		for _, id := range pre {
			if id == "" || !isIdent(id) {
				return Version{}, ErrSyntax
			}
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, ErrSyntax
	}
	var nums [3]uint64
	for i, p := range parts {
		if p == "" || !allDigits(p) || (len(p) > 1 && p[0] == '0') {
			return Version{}, ErrSyntax
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return Version{}, ErrSyntax
		}
		nums[i] = n
	}
	return Version{raw: raw, nums: nums, pre: pre, hasPre: pre != nil}, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isIdent(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' || r == '-') {
			return false
		}
	}
	return true
}

// Compare returns -1, 0, 1 according to semantic-version ordering.
func Compare(a, b Version) int {
	for i := 0; i < 3; i++ {
		if a.nums[i] != b.nums[i] {
			if a.nums[i] < b.nums[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case !a.hasPre && !b.hasPre:
		return 0
	case !a.hasPre:
		return 1
	case !b.hasPre:
		return -1
	}
	return comparePre(a.pre, b.pre)
}

func comparePre(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compareIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func compareIdent(a, b string) int {
	an, bn := allDigits(a), allDigits(b)
	switch {
	case an && bn:
		x, _ := strconv.ParseUint(a, 10, 64)
		y, _ := strconv.ParseUint(b, 10, 64)
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		return 0
	case an:
		return -1 // numeric identifiers sort before alphanumeric
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

// String returns the original canonical text.
func (v Version) String() string { return v.raw }
