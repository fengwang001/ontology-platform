package semver

import (
	"errors"
	"strconv"
)

// Version 表示一个 SemVer 2.0.0 版本。
type Version struct {
	Major    int
	Minor    int
	Patch    int
	Pre      []string // 预发布标识符（点分），无预发布时为 nil
	Build    string   // build metadata 原文（不含 '+'），无则为空
	original string   // Parse 的原始输入，供 String 原样还原
	hasPre   bool
}

// String 原样还原 Parse 的输入（含 build metadata）。
func (v Version) String() string { return v.original }

// Core 返回 major.minor.patch 三元组。
func (v Version) Core() (major, minor, patch int) {
	return v.Major, v.Minor, v.Patch
}

// Prerelease 报告该版本是否带预发布后缀。
func (v Version) Prerelease() bool { return v.hasPre }

// Parse 解析 major.minor.patch[-prerelease][+build]。
func Parse(s string) (Version, error) {
	v := Version{original: s}

	rest := s
	if i := indexByte(rest, '+'); i >= 0 {
		build := rest[i+1:]
		rest = rest[:i]
		if err := validateIdentifiers(build, true); err != nil {
			return Version{}, &ParseError{Kind: ErrInvalidVersion, Input: s, Msg: err.Error()}
		}
		v.Build = build
	}

	if i := indexByte(rest, '-'); i >= 0 {
		v.hasPre = true
		v.Pre = splitDots(rest[i+1:])
		if err := validateIdentifiers(rest[i+1:], false); err != nil {
			return Version{}, &ParseError{Kind: ErrInvalidVersion, Input: s, Msg: err.Error()}
		}
		rest = rest[:i]
	}

	nums := splitDots(rest)
	if len(nums) != 3 {
		return Version{}, &ParseError{Kind: ErrInvalidVersion, Input: s, Msg: "want major.minor.patch"}
	}
	var err error
	if v.Major, err = parseCoreNumber(nums[0]); err != nil {
		return Version{}, &ParseError{Kind: ErrInvalidVersion, Input: s, Msg: err.Error()}
	}
	if v.Minor, err = parseCoreNumber(nums[1]); err != nil {
		return Version{}, &ParseError{Kind: ErrInvalidVersion, Input: s, Msg: err.Error()}
	}
	if v.Patch, err = parseCoreNumber(nums[2]); err != nil {
		return Version{}, &ParseError{Kind: ErrInvalidVersion, Input: s, Msg: err.Error()}
	}
	return v, nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func splitDots(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func parseCoreNumber(s string) (int, error) {
	if s == "" || !isAllDigits(s) {
		return 0, errors.New("version core part must be decimal digits")
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, errors.New("version core part must not have leading zero")
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func isAllDigits(s string) bool {
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
