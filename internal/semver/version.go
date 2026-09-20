package semver

import (
	"strconv"
	"strings"
)

// PreIdent 是预发布版本的一个点分标识符。
type PreIdent struct {
	// Numeric 表示该标识符是否为纯数字段。
	Numeric bool
	// Num 在 Numeric 为 true 时保存其数值。
	Num uint64
	// Text 保存标识符的原始文本（数字段也保留）。
	Text string
}

// Version 表示一个 SemVer 2.0.0 版本。
type Version struct {
	Major, Minor, Patch uint64
	// Pre 为预发布标识符序列，长度为 0 表示没有预发布段。
	Pre []PreIdent
	// Build 是 build metadata 的原始文本（不含开头的 '+'），可能为空。
	Build string

	// raw 保存 Parse 输入，用于 String 原样还原。
	raw string
}

// HasPre 报告版本是否带有预发布段。
func (v Version) HasPre() bool { return len(v.Pre) > 0 }

// String 原样还原 Parse 的输入；非 Parse 构造的版本则按规范重建。
func (v Version) String() string {
	if v.raw != "" {
		return v.raw
	}
	var b strings.Builder
	b.WriteString(strconv.FormatUint(v.Major, 10))
	b.WriteByte('.')
	b.WriteString(strconv.FormatUint(v.Minor, 10))
	b.WriteByte('.')
	b.WriteString(strconv.FormatUint(v.Patch, 10))
	if len(v.Pre) > 0 {
		b.WriteByte('-')
		for i, id := range v.Pre {
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(id.Text)
		}
	}
	if v.Build != "" {
		b.WriteByte('+')
		b.WriteString(v.Build)
	}
	return b.String()
}

// Parse 按 SemVer 2.0.0 解析版本字符串。
func Parse(s string) (Version, error) {
	orig := s
	build := ""
	if i := strings.IndexByte(s, '+'); i >= 0 {
		build = s[i+1:]
		s = s[:i]
		if err := validateBuild(build); err != nil {
			return Version{}, &VersionParseError{Input: orig, Reason: err.Error()}
		}
	}

	core := s
	var prePart string
	hasPre := false
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core = s[:i]
		prePart = s[i+1:]
		hasPre = true
	}

	nums := strings.Split(core, ".")
	if len(nums) != 3 {
		return Version{}, &VersionParseError{Input: orig, Reason: "major.minor.patch must have exactly 3 fields"}
	}
	v := Version{Build: build, raw: orig}
	parts := [3]*uint64{&v.Major, &v.Minor, &v.Patch}
	for i, p := range nums {
		n, ok := parseCoreNumber(p)
		if !ok {
			return Version{}, &VersionParseError{Input: orig, Reason: "invalid numeric field: " + p}
		}
		*parts[i] = n
	}

	if hasPre {
		ids, err := parsePreRelease(prePart)
		if err != nil {
			return Version{}, &VersionParseError{Input: orig, Reason: err.Error()}
		}
		v.Pre = ids
	}
	return v, nil
}

func parseCoreNumber(p string) (uint64, bool) {
	if p == "" || !allDigits(p) {
		return 0, false
	}
	if len(p) > 1 && p[0] == '0' {
		return 0, false
	}
	n, err := strconv.ParseUint(p, 10, 64)
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

func validIdentChars(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') || c == '-' {
			continue
		}
		return false
	}
	return true
}

func validateBuild(b string) error {
	if b == "" {
		return errEmptyIdent
	}
	for _, id := range strings.Split(b, ".") {
		if id == "" || !validIdentChars(id) {
			return errBadIdent
		}
	}
	return nil
}

var (
	errEmptyIdent = stringError("empty identifier")
	errBadIdent   = stringError("identifier contains invalid characters")
)

type stringError string

func (e stringError) Error() string { return string(e) }

func parsePreRelease(p string) ([]PreIdent, error) {
	if p == "" {
		return nil, errEmptyIdent
	}
	rawIDs := strings.Split(p, ".")
	ids := make([]PreIdent, 0, len(rawIDs))
	for _, id := range rawIDs {
		if id == "" || !validIdentChars(id) {
			return nil, errBadIdent
		}
		pi := PreIdent{Text: id}
		if allDigits(id) {
			if len(id) > 1 && id[0] == '0' {
				return nil, stringError("numeric identifier must not contain leading zeros")
			}
			n, err := strconv.ParseUint(id, 10, 64)
			if err != nil {
				return nil, stringError("numeric identifier out of range")
			}
			pi.Numeric = true
			pi.Num = n
		}
		ids = append(ids, pi)
	}
	return ids, nil
}
