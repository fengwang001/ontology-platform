package ver

import (
	"errors"
	"strconv"
	"strings"
)

// ErrInvalidVersion 表示版本字符串语法非法，可判定（errors.Is）。
var ErrInvalidVersion = errors.New("invalid version string")

// Version 是语义化版本：主.次.修[-预发布]。可比较、可打印。
type Version struct {
	major, minor, patch int
	pre                 []preID
}

type preID struct {
	numeric bool
	num     int
	str     string
}

// Parse 解析 "1.2.3" 或 "1.2.3-beta.1"。
func Parse(s string) (Version, error) {
	if s == "" || strings.ContainsAny(s, " \t+") {
		return Version{}, ErrInvalidVersion
	}
	dash := strings.IndexByte(s, '-')
	core := s
	prePart := ""
	if dash >= 0 {
		core, prePart = s[:dash], s[dash+1:]
		if prePart == "" {
			return Version{}, ErrInvalidVersion
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, ErrInvalidVersion
	}
	nums := [3]int{}
	for i, p := range parts {
		n, ok := parseNum(p)
		if !ok {
			return Version{}, ErrInvalidVersion
		}
		nums[i] = n
	}
	v := Version{major: nums[0], minor: nums[1], patch: nums[2]}
	if dash >= 0 {
		for _, id := range strings.Split(prePart, ".") {
			pid, ok := parsePreID(id)
			if !ok {
				return Version{}, ErrInvalidVersion
			}
			v.pre = append(v.pre, pid)
		}
	}
	return v, nil
}

// MustParse 解析失败时 panic，便于构造测试/演示数据。
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Compare 严格序：v < o 返回 -1，相等 0，大于 1。
func (v Version) Compare(o Version) int {
	for _, pair := range [][2]int{
		{v.major, o.major}, {v.minor, o.minor}, {v.patch, o.patch},
	} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	return comparePre(v.pre, o.pre)
}

func (v Version) String() string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(v.major))
	b.WriteByte('.')
	b.WriteString(strconv.Itoa(v.minor))
	b.WriteByte('.')
	b.WriteString(strconv.Itoa(v.patch))
	if len(v.pre) > 0 {
		b.WriteByte('-')
		for i, id := range v.pre {
			if i > 0 {
				b.WriteByte('.')
			}
			if id.numeric {
				b.WriteString(strconv.Itoa(id.num))
			} else {
				b.WriteString(id.str)
			}
		}
	}
	return b.String()
}

// IsPrerelease 报告是否带预发布标识。
func (v Version) IsPrerelease() bool { return len(v.pre) > 0 }

func parseNum(s string) (int, bool) {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func parsePreID(s string) (preID, bool) {
	if s == "" {
		return preID{}, false
	}
	allDigit := true
	for _, r := range s {
		if r < '0' || r > '9' {
			allDigit = false
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
				return preID{}, false
			}
		}
	}
	if allDigit {
		if len(s) > 1 && s[0] == '0' {
			return preID{}, false
		}
		n, _ := strconv.Atoi(s)
		return preID{numeric: true, num: n}, true
	}
	return preID{str: s}, true
}

func comparePre(a, b []preID) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1 // 正式版 > 预发布
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := a[i], b[i]
		switch {
		case x.numeric && !y.numeric:
			return -1
		case !x.numeric && y.numeric:
			return 1
		case x.numeric && y.numeric:
			if x.num != y.num {
				if x.num < y.num {
					return -1
				}
				return 1
			}
		case x.str != y.str:
			return strings.Compare(x.str, y.str)
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
