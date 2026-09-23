package vv

import (
	"sort"
	"strings"
)

// Vector 是副本 ID -> 计数器的版本向量。零值为空向量（所有分量为 0）。
// 缺失分量一律按 0 参与比较。
type Vector map[string]uint64

// Relation 描述两个版本向量之间的四种偏序关系之一。
type Relation int

const (
	// RelEqual 两向量在分量并集上完全相等。
	RelEqual Relation = iota
	// RelBefore 接收者严格小于参数：因果在前。
	RelBefore
	// RelAfter 接收者严格大于参数：因果在后。
	RelAfter
	// RelConcurrent 并发：同时存在更小与更大分量，互不可比。
	RelConcurrent
)

func (r Relation) String() string {
	switch r {
	case RelEqual:
		return "equal"
	case RelBefore:
		return "before"
	case RelAfter:
		return "after"
	default:
		return "concurrent"
	}
}

// Compare 返回 a 相对 b 的偏序关系。
// 代价与两向量分量并集大小成正比：每个并集键恰好读取两侧各一次，
// 即 Counter 记录的读取次数 <= 2*|union|，不遍历任何全局副本表。
func Compare(a, b Vector) Relation {
	return CompareCounted(a, b, nil)
}

// CompareCounted 与 Compare 相同，但把分量读取次数计入 c（可为 nil）。
func CompareCounted(a, b Vector, c *Counter) Relation {
	var lt, gt bool
	for _, key := range unionKeys(a, b) {
		x := a[key]
		z := b[key]
		if c != nil {
			c.Add(2)
		}
		if x < z {
			lt = true
		}
		if x > z {
			gt = true
		}
	}
	switch {
	case lt && gt:
		return RelConcurrent
	case lt:
		return RelBefore
	case gt:
		return RelAfter
	default:
		return RelEqual
	}
}

func unionKeys(a, b Vector) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Equal 报告两向量在缺失分量按 0 处理后是否逐分量相等。
func (v Vector) Equal(o Vector) bool { return Compare(v, o) == RelEqual }

// Clone 返回深拷贝。
func (v Vector) Clone() Vector {
	out := make(Vector, len(v))
	for k, val := range v {
		out[k] = val
	}
	return out
}

// Canonical 返回确定性的规范化字节串（按 ID 字典序），用于排序与哈希。
func (v Vector) Canonical() string {
	var sb strings.Builder
	for _, k := range sortedKeys(v) {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(itoa(v[k]))
		sb.WriteByte(';')
	}
	return sb.String()
}

func sortedKeys(v Vector) []string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
