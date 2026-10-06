package exports

import "strings"

// wildKey 把一个通配键拆成星号前后的两个字面量部分。
// 键中至多一个 "*"（构造时已校验），所以拆分唯一。
type wildKey struct {
	prefix string
	suffix string
}

// index 是子路径查找索引。
//
// 设计要点：解析开销不得随键总数增长。
//   - 精确键放在哈希表 exact 中，一次探测命中。
//   - 通配键放在哈希表 wild 中，键为 (prefix, suffix) 对。
//     查找时枚举请求子路径的所有拆分 subpath = prefix + matched + suffix
//     （matched 至少 1 个字符），按"前缀最长、其次整键最长"的优先级顺序
//     逐个探测哈希表，第一个命中即最优。拆分数量只取决于请求长度 n：
//     至多 n(n+1)/2 次探测，与表中键的总数无关。
type index struct {
	exact map[string]Target
	wild  map[wildKey]Target
}

func newIndex() index {
	return index{
		exact: make(map[string]Target),
		wild:  make(map[wildKey]Target),
	}
}

func (ix index) add(key string, t Target) {
	if star := strings.IndexByte(key, '*'); star >= 0 {
		ix.wild[wildKey{prefix: key[:star], suffix: key[star+1:]}] = t
		return
	}
	ix.exact[key] = t
}

// lookup 查找 subpath 命中的键。
// 返回命中的键、对应目标、哈希探测次数（供复杂度验证）与是否命中。
func (ix index) lookup(subpath string) (key string, tgt Target, probes int, ok bool) {
	probes++
	if t, hit := ix.exact[subpath]; hit {
		return subpath, t, probes, true
	}
	n := len(subpath)
	// i 从大到小：前缀越长越优先；j 从小到大：后缀越长（整键越长）越优先。
	// matched = subpath[i:j]，要求 j >= i+1 保证星号至少匹配一个字符。
	for i := n; i >= 0; i-- {
		for j := i + 1; j <= n; j++ {
			probes++
			if t, hit := ix.wild[wildKey{prefix: subpath[:i], suffix: subpath[j:]}]; hit {
				return subpath[:i] + "*" + subpath[j:], t, probes, true
			}
		}
	}
	return "", Target{}, probes, false
}
