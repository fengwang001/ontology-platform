// Package match 流式 KMP 匹配状态机：持有模式串、π 与当前态 j，
// Feed 逐批喂入文本，返回本批新产生的匹配结束下标（绝对下标，允许重叠）。
package match

import (
	"errors"

	"ontology/pfx"
)

// ErrTooManyMatches 表示一批 Feed 会使累计匹配数超过 maxMatches，整批不生效。
var ErrTooManyMatches = errors.New("match: match count exceeds maxMatches")

// Matcher 是单模式流式匹配器。不是并发安全的，并发包装由上层负责。
type Matcher struct {
	pat        []byte
	pi         []int
	j          int   // 当前匹配态：已消费文本的最长后缀 == pat 前缀的长度
	consumed   int   // 已提交的消费字节总数
	matches    []int // 已收集的匹配结束下标（绝对下标）
	cmps       int64 // 匹配阶段字符比较总次数（非导出，仅供内部测试核验均摊复杂度）
	maxMatches int
}

// New 构造匹配器；pat 会被拷贝，之后调用方的修改不影响匹配器。
func New(pat []byte, maxMatches int) *Matcher {
	cp := append([]byte(nil), pat...)
	return &Matcher{pat: cp, pi: pfx.Compute(cp), maxMatches: maxMatches}
}

// Feed 喂入一批字节，返回本批内新产生的匹配结束下标（绝对下标）。
// 空 chunk 合法，返回空。若本批会使累计匹配数超过 maxMatches，
// 返回 ErrTooManyMatches 且整批不生效：j、consumed、matches、cmps 全部不变。
func (m *Matcher) Feed(chunk []byte) ([]int, error) {
	// 全程在局部变量上模拟，确认不超限后才一次性提交，保证失败不留痕。
	j, pos, cmps := m.j, m.consumed, m.cmps
	var fresh []int
	for _, c := range chunk {
		for {
			cmps++
			if m.pat[j] == c {
				j++
				break
			}
			if j == 0 {
				break
			}
			j = m.pi[j-1]
		}
		if j == len(m.pat) {
			fresh = append(fresh, pos)
			j = m.pi[j-1] // 继续找重叠匹配
		}
		pos++
	}
	if len(m.matches)+len(fresh) > m.maxMatches {
		return nil, ErrTooManyMatches
	}
	m.j, m.consumed, m.cmps = j, pos, cmps
	m.matches = append(m.matches, fresh...)
	return fresh, nil
}

// Matches 返回已收集的全部匹配结束下标的副本。
func (m *Matcher) Matches() []int {
	return append([]int(nil), m.matches...)
}
