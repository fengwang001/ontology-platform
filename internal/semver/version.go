package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version 是一个解析后的 SemVer 2.0.0 版本。
type Version struct {
	Major uint64
	Minor uint64
	Patch uint64
	// Pre 是预发布标识符序列，无预发布时为 nil。
	Pre []string
	// Build 是 build metadata（不含 '+'），不参与比较。
	Build string

	raw string
}

// Parse 解析 major.minor.patch[-prerelease][+build] 形式的版本串。
func Parse(s string) (Version, error) {
	v := Version{raw: s}
	rest := s
	if i := strings.IndexByte(rest, '+'); i >= 0 {
		v.Build = rest[i+1:]
		rest = rest[:i]
		if err := checkIdents(v.Build, true); err != nil {
			return Version{}, fmt.Errorf("%w %q: build: %v", ErrInvalidVersion, s, err)
		}
	}
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		pre := rest[i+1:]
		rest = rest[:i]
		if err := checkIdents(pre, false); err != nil {
			return Version{}, fmt.Errorf("%w %q: prerelease: %v", ErrInvalidVersion, s, err)
		}
		v.Pre = strings.Split(pre, ".")
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%w %q: want major.minor.patch", ErrInvalidVersion, s)
	}
	nums := make([]uint64, 3)
	for i, p := range parts {
		n, err := parseNumeric(p)
		if err != nil {
			return Version{}, fmt.Errorf("%w %q: %v", ErrInvalidVersion, s, err)
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, nil
}

// String 原样返回解析时的输入串（含 build metadata）。
func (v Version) String() string { return v.raw }

// parseNumeric 解析无前导零的十进制数。
func parseNumeric(s string) (uint64, error) {
	if s == "" || !isDigits(s) {
		return 0, fmt.Errorf("bad numeric identifier %q", s)
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("leading zero in %q", s)
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("numeric identifier %q out of range", s)
	}
	return n, nil
}

// checkIdents 校验点分标识符序列；allowZeroPadded 为 true 时（build
// metadata）允许纯数字段带前导零。
func checkIdents(s string, allowZeroPadded bool) error {
	if s == "" {
		return fmt.Errorf("empty identifier list")
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return fmt.Errorf("empty identifier")
		}
		for i := 0; i < len(id); i++ {
			if !isIdentChar(id[i]) {
				return fmt.Errorf("bad character in %q", id)
			}
		}
		if !allowZeroPadded && isDigits(id) && len(id) > 1 && id[0] == '0' {
			return fmt.Errorf("leading zero in numeric identifier %q", id)
		}
	}
	return nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isIdentChar(c byte) bool {
	return c == '-' ||
		(c >= '0' && c <= '9') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z')
}
