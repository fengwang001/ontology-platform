package depsolver

import (
	"strconv"
	"strings"
)

// Version 表示一个语义版本 X.Y.Z[-pre]。
type Version struct {
	major      int
	minor      int
	patch      int
	prerelease []prereleaseID
	raw        string
}

type prereleaseID struct {
	numeric bool
	num     int
	text    string
}

const versionPartMax = 9999

// ParseVersion 解析并严格校验版本字符串。
func ParseVersion(s string) (Version, error) {
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, ErrInvalidVersion
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, ok := parseVersionNumber(p)
		if !ok || n > versionPartMax {
			return Version{}, ErrInvalidVersion
		}
		nums[i] = n
	}
	var ids []prereleaseID
	if hasPre {
		if pre == "" {
			return Version{}, ErrInvalidVersion
		}
		for _, raw := range strings.Split(pre, ".") {
			id, err := parsePrereleaseID(raw)
			if err != nil {
				return Version{}, err
			}
			ids = append(ids, id)
		}
	}
	return Version{
		major:      nums[0],
		minor:      nums[1],
		patch:      nums[2],
		prerelease: ids,
		raw:        s,
	}, nil
}

func parseVersionNumber(s string) (int, bool) {
	if s == "" || !allDigits(s) {
		return 0, false
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isAlnumOrHyphen(b byte) bool {
	return b == '-' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

func parsePrereleaseID(raw string) (prereleaseID, error) {
	if raw == "" {
		return prereleaseID{}, ErrInvalidVersion
	}
	if allDigits(raw) {
		if len(raw) > 1 && raw[0] == '0' {
			return prereleaseID{}, ErrInvalidVersion
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return prereleaseID{}, ErrInvalidVersion
		}
		return prereleaseID{numeric: true, num: n}, nil
	}
	for i := 0; i < len(raw); i++ {
		if !isAlnumOrHyphen(raw[i]) {
			return prereleaseID{}, ErrInvalidVersion
		}
	}
	return prereleaseID{numeric: false, text: raw}, nil
}

// String 返回版本的原始字符串。
func (v Version) String() string { return v.raw }

func (v Version) hasPrerelease() bool { return len(v.prerelease) > 0 }

func (v Version) coreEquals(o Version) bool {
	return v.major == o.major && v.minor == o.minor && v.patch == o.patch
}

// CompareVersion 返回 -1/0/1，表示 a 小于/等于/大于 b。
func CompareVersion(a, b Version) int {
	if c := compareInt(a.major, b.major); c != 0 {
		return c
	}
	if c := compareInt(a.minor, b.minor); c != 0 {
		return c
	}
	if c := compareInt(a.patch, b.patch); c != 0 {
		return c
	}
	return comparePrerelease(a.prerelease, b.prerelease)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func comparePrerelease(a, b []prereleaseID) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := a[i], b[i]
		switch {
		case x.numeric && y.numeric:
			if c := compareInt(x.num, y.num); c != 0 {
				return c
			}
		case x.numeric:
			return -1
		case y.numeric:
			return 1
		default:
			switch {
			case x.text < y.text:
				return -1
			case x.text > y.text:
				return 1
			}
		}
	}
	return compareInt(len(a), len(b))
}
