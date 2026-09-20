package partscan

// prefixMatcher 是一个有状态的 KMP 子串匹配器：逐字节调用 update 推进
// 匹配状态，返回值等于模式串长度时本字节恰好是一次完整匹配的最后一个
// 字节。状态由 Scanner 跨块持有，因此分隔符被切在任意两个块之间都能
// 正确匹配。
type prefixMatcher struct {
	pattern string
	next    []int // next[i]：pattern[:i] 的最长相等真前后缀长度
}

func newPrefixMatcher(pattern string) prefixMatcher {
	m := prefixMatcher{pattern: pattern, next: make([]int, len(pattern)+1)}
	m.next[0] = -1
	for i := 1; i <= len(pattern); i++ {
		k := m.next[i-1]
		for k >= 0 && pattern[k] != pattern[i-1] {
			k = m.next[k]
		}
		m.next[i] = k + 1
	}
	return m
}

func (m *prefixMatcher) length() int { return len(m.pattern) }

// update 消费一个字节，返回消费后匹配到的模式串前缀长度。
func (m *prefixMatcher) update(q int, c byte) int {
	for q >= 0 && m.pattern[q] != c {
		q = m.next[q]
	}
	return q + 1
}
