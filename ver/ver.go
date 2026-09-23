// Package ver parses and compares semantic versions: major.minor.patch
// with an optional dot-separated prerelease suffix.
package ver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidVersion reports a version string that violates the grammar
// "major.minor.patch[-prerelease]".
var ErrInvalidVersion = errors.New("ver: invalid version")

// Version is a parsed semantic version. The zero value is unusable;
// construct via Parse.
type Version struct {
	major, minor, patch uint64
	pre                 []string // nil when no prerelease
	raw                 string
}

// Parse parses s as major.minor.patch with optional "-pre.release" suffix.
func Parse(s string) (Version, error) {
	var v Version
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("%w: %q", ErrInvalidVersion, s)
	}
	nums := make([]uint64, 3)
	for i, p := range parts {
		n, err := parseNum(p)
		if err != nil {
			return v, fmt.Errorf("%w: %q", ErrInvalidVersion, s)
		}
		nums[i] = n
	}
	if hasPre {
		ids := strings.Split(pre, ".")
		for _, id := range ids {
			if !validIdent(id) {
				return v, fmt.Errorf("%w: %q", ErrInvalidVersion, s)
			}
		}
		v.pre = ids
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]
	v.raw = s
	return v, nil
}

func parseNum(s string) (uint64, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, errors.New("bad number")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errors.New("bad number")
		}
	}
	return strconv.ParseUint(s, 10, 64)
}

func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
			return false
		}
	}
	return true
}

// String returns the original version text.
func (v Version) String() string { return v.raw }

// Compare returns -1, 0 or +1 as v is less than, equal to, or greater
// than o. A version with a prerelease is lower than the same version
// without one (semver §11).
func (v Version) Compare(o Version) int {
	if c := cmpUint(v.major, o.major); c != 0 {
		return c
	}
	if c := cmpUint(v.minor, o.minor); c != 0 {
		return c
	}
	if c := cmpUint(v.patch, o.patch); c != 0 {
		return c
	}
	switch {
	case v.pre == nil && o.pre == nil:
		return 0
	case v.pre == nil:
		return 1
	case o.pre == nil:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(o.pre); i++ {
		if c := cmpIdent(v.pre[i], o.pre[i]); c != 0 {
			return c
		}
	}
	return cmpUint(uint64(len(v.pre)), uint64(len(o.pre)))
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpIdent(a, b string) int {
	na, errA := strconv.ParseUint(a, 10, 64)
	nb, errB := strconv.ParseUint(b, 10, 64)
	numA, numB := errA == nil && isAllDigits(a), errB == nil && isAllDigits(b)
	switch {
	case numA && numB:
		return cmpUint(na, nb)
	case numA:
		return -1 // numeric identifiers sort below alphanumeric
	case numB:
		return 1
	}
	return strings.Compare(a, b)
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}
