package refstore

import "strings"

const (
	branchPrefix = "refs/heads/"
	tagPrefix    = "refs/tags/"
)

// namespace 是引用名所属的命名空间。
type namespace int

const (
	nsBranch namespace = iota
	nsTag
)

// splitNamespace 拆分引用的命名空间与名字部分。
// 名字落在分支、标签两个命名空间之外时返回 false（参数非法）。
func splitNamespace(ref string) (namespace, string, bool) {
	if rest, ok := strings.CutPrefix(ref, branchPrefix); ok {
		return nsBranch, rest, true
	}
	if rest, ok := strings.CutPrefix(ref, tagPrefix); ok {
		return nsTag, rest, true
	}
	return 0, "", false
}

// validRefName 校验完整引用名：非空、不以分隔符开头或结尾、不含连续分隔符、
// 不含控制字节、任一分段不以点开头且不以 ".lock" 结尾。
func validRefName(ref string) bool {
	if ref == "" {
		return false
	}
	for i := 0; i < len(ref); i++ {
		if c := ref[i]; c < 0x20 || c == 0x7f {
			return false
		}
	}
	for _, seg := range strings.Split(ref, "/") {
		if seg == "" { // 空分段：首末分隔符或连续分隔符
			return false
		}
		if seg[0] == '.' {
			return false
		}
		if strings.HasSuffix(seg, ".lock") {
			return false
		}
	}
	return true
}

// namesConflict 报告两个引用名是否构成祖先分段前缀冲突：
// 一个名字与另一个名字加分隔符的前缀相同。同名不算冲突（由重复检查处理）。
func namesConflict(a, b string) bool {
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
