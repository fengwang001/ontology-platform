// Package match 实现 Rabin-Karp 子串匹配：滚动哈希粗筛 + 逐字符验证。
package match

import (
	"errors"
	"sync/atomic"

	"ontology/hash"
)

// ErrEmptyPattern 表示 pattern 为空。
var ErrEmptyPattern = errors.New("match: empty pattern")

// Base 与 Mod 是滚动哈希参数：h*Base+c < 1e17 << 2^64，uint64 无溢出。
const (
	Base = 91138233
	Mod  = 1000000007
)

var verifyCount atomic.Int64

// VerifyCount 返回逐字符验证的累计次数（复杂度上界断言用）。
func VerifyCount() int64 { return verifyCount.Load() }

// ResetVerifyCount 清零验证计数器。
func ResetVerifyCount() { verifyCount.Store(0) }

// FindAll 返回 pattern 在 text 中所有出现位置（升序，含重叠）。
// 空 pattern 返回 ErrEmptyPattern；pattern 长于 text 返回空结果。
func FindAll(text, pattern string) ([]int, error) {
	m := len(pattern)
	if m == 0 {
		return nil, ErrEmptyPattern
	}
	if m > len(text) {
		return nil, nil
	}
	ph := hash.New(Base, Mod, m)
	for i := 0; i < m; i++ {
		ph.Append(pattern[i])
	}
	target := ph.Value()
	wh := hash.New(Base, Mod, m)
	for i := 0; i < m; i++ {
		wh.Append(text[i])
	}
	var out []int
	for i := 0; i+m <= len(text); i++ {
		if wh.Value() == target && verify(text[i:i+m], pattern) {
			out = append(out, i)
		}
		if i+m < len(text) {
			wh.Remove(text[i])
			wh.Append(text[i+m])
		}
	}
	return out, nil
}

// verify 哈希命中后逐字符验证，杜绝碰撞造成的假阳性。
func verify(s, p string) bool {
	for j := 0; j < len(p); j++ {
		verifyCount.Add(1)
		if s[j] != p[j] {
			return false
		}
	}
	return true
}
