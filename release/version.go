package release

import (
	"fmt"
	"strconv"
	"strings"
)

// MaxComponent 是版本各分量与预发布序号允许的最大值。
const MaxComponent = 999999

// Core 是版本的三个数值分量 (M, m, p)。
type Core struct {
	Major, Minor, Patch int
}

// Version 表示一个语义化版本：核心分量加可选预发布通道与序号。
// Channel 为空表示稳定版本；非空时 N >= 1。
type Version struct {
	Core    Core
	Channel string
	N       int
}

// Commit 是一次约定式提交：类型与是否 breaking。
type Commit struct {
	Type     string
	Breaking bool
}

// Reason 是操作被拒绝的可区分原因。
type Reason string

const (
	// ErrInvalidVersion：Tag 版本串非法。
	ErrInvalidVersion Reason = "invalid version"
	// ErrAlreadyTagged：相同版本已登记过。
	ErrAlreadyTagged Reason = "version already tagged"
	// ErrInvalidChannel：通道名非法。
	ErrInvalidChannel Reason = "invalid channel"
	// ErrInvalidCommitType：提交类型非法（按下标最小者）。
	ErrInvalidCommitType Reason = "invalid commit type"
	// ErrNoRelease：没有可发布的核心。
	ErrNoRelease Reason = "no releasable target"
	// ErrOverflow：目标核心分量或结果 N 超过 MaxComponent。
	ErrOverflow Reason = "version component overflow"
)

// Error 携带拒绝原因与可读信息，实现 error。
type Error struct {
	Reason Reason
	detail string
}

func (e *Error) Error() string {
	return string(e.Reason) + ": " + e.detail
}

func fail(reason Reason, format string, args ...any) *Error {
	return &Error{Reason: reason, detail: fmt.Sprintf(format, args...)}
}

// ParseVersion 解析「M.m.p」或「M.m.p-c.N」形式的版本串。
func ParseVersion(s string) (Version, error) {
	parts := strings.SplitN(s, "-", 2)
	core, err := parseCore(parts[0])
	if err != nil {
		return Version{}, err
	}

	v := Version{Core: core}
	if len(parts) == 2 {
		suffix := strings.Split(parts[1], ".")
		if len(suffix) != 2 || !validChannel(suffix[0]) {
			return Version{}, fmt.Errorf("invalid prerelease suffix %q", parts[1])
		}
		n, err := parseComponent(suffix[1], 1)
		if err != nil {
			return Version{}, err
		}
		v.Channel = suffix[0]
		v.N = n
	}
	return v, nil
}

func parseCore(s string) (Core, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Core{}, fmt.Errorf("core must have three components: %q", s)
	}
	nums := [3]int{}
	for i, p := range parts {
		n, err := parseComponent(p, 0)
		if err != nil {
			return Core{}, err
		}
		nums[i] = n
	}
	return Core{nums[0], nums[1], nums[2]}, nil
}

// parseComponent 解析无前置零、范围在 [min, MaxComponent] 的十进制整数。
func parseComponent(s string, min int) (int, error) {
	if s == "" || len(s) > len(strconv.Itoa(MaxComponent)) {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("invalid number %q", s)
		}
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("leading zero not allowed in %q", s)
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min || n > MaxComponent {
		return 0, fmt.Errorf("number %q out of range [%d,%d]", s, min, MaxComponent)
	}
	return n, nil
}

// validChannel 判断通道名是否为 1 到 16 个小写字母。
func validChannel(c string) bool {
	if len(c) < 1 || len(c) > 16 {
		return false
	}
	for i := 0; i < len(c); i++ {
		if c[i] < 'a' || c[i] > 'z' {
			return false
		}
	}
	return true
}

// validCommitType 判断提交类型是否为非空小写字母串。
func validCommitType(t string) bool {
	if t == "" {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] < 'a' || t[i] > 'z' {
			return false
		}
	}
	return true
}

// String 渲染版本串。
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Core.Major, v.Core.Minor, v.Core.Patch)
	if v.Channel != "" {
		s += fmt.Sprintf("-%s.%d", v.Channel, v.N)
	}
	return s
}

// Stable 报告该版本是否为稳定版本（无预发布通道）。
func (v Version) Stable() bool { return v.Channel == "" }

// CompareCore 按数值比较两个核心，返回 -1/0/1。
func CompareCore(a, b Core) int {
	switch {
	case a.Major != b.Major:
		return cmpInt(a.Major, b.Major)
	case a.Minor != b.Minor:
		return cmpInt(a.Minor, b.Minor)
	default:
		return cmpInt(a.Patch, b.Patch)
	}
}

// CompareVersion 按 Versions 的排序规则比较两个版本，返回 -1/0/1。
func CompareVersion(a, b Version) int {
	if c := CompareCore(a.Core, b.Core); c != 0 {
		return c
	}
	switch {
	case a.Channel == b.Channel:
		return cmpInt(a.N, b.N)
	case a.Channel == "":
		return 1 // 同核心：预发布 < 稳定
	case b.Channel == "":
		return -1
	default:
		if a.Channel < b.Channel {
			return -1
		}
		return 1
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
