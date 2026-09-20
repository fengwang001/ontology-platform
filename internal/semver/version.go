package semver

// Version 表示一个 SemVer 2.0.0 语义化版本。
type Version struct {
	major      int
	minor      int
	patch      int
	prerelease []string
	build      []string
	raw        string
}

// Parse 解析 SemVer 2.0.0 版本字符串。
func Parse(s string) (Version, error) {
	return Version{}, nil
}

// String 原样还原 Parse 的输入。
func (v Version) String() string {
	return v.raw
}

// Compare 按 SemVer 2.0.0 优先级规则比较两个版本，返回 -1/0/1。
func Compare(a, b Version) int {
	return 0
}
