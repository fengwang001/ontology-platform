// Package match 用 Rabin-Karp 滚动哈希定位 pattern 的所有出现位置。
package match

import (
	"errors"
	"sync/atomic"

	"ontology/hash"
)

// ErrEmptyPattern 表示 pattern 为空字符串。
var ErrEmptyPattern = errors.New("match: empty pattern")

const (
	// Base 是滚动哈希的固定底数。
	Base = 911_371
	// Mod 是滚动哈希的固定模数（大质数，正常输入碰撞极少）。
	Mod = 1_000_003

	base = Base
	mod  = Mod
)

// verifyCount 统计哈希命中后逐字符验证的比较次数，-race 下并发安全。
var verifyCount atomic.Int64

// VerifyCount 返回自进程启动以来逐字符验证的累计比较次数。
func VerifyCount() int64 { return verifyCount.Load() }

// ResetVerifyCount 把逐字符验证计数清零。
func ResetVerifyCount() { verifyCount.Store(0) }

// FindAll 返回 pattern 在 text 中所有出现位置（升序，含重叠）。
// pattern 长于 text 时返回 nil；pattern 为空返回 ErrEmptyPattern。
func FindAll(text, pattern string) ([]int, error) {
	m := len(pattern)
	if m == 0 {
		return nil, ErrEmptyPattern
	}
	n := len(text)
	if m > n {
		return nil, nil
	}

	pow, err := hash.Power(base, mod, uint64(m)-1)
	if err != nil {
		return nil, err
	}
	ph, err := hash.New(base, mod)
	if err != nil {
		return nil, err
	}
	wh, err := hash.New(base, mod)
	if err != nil {
		return nil, err
	}
	for i := 0; i < m; i++ {
		ph.Append(pattern[i])
		wh.Append(text[i])
	}

	var hits []int
	for start := 0; start <= n-m; start++ {
		if wh.Value() == ph.Value() {
			compared := verifyAt(text, pattern, start)
			verifyCount.Add(int64(compared))
			if compared == m {
				hits = append(hits, start)
			}
		}
		if start < n-m {
			if err := wh.Slide(text[start], text[start+m], pow); err != nil {
				return nil, err
			}
		}
	}
	return hits, nil
}

// verifyAt 逐字符比较 text[pos:pos+m] 与 pattern，返回首个不一致前的比较次数。
func verifyAt(text, pattern string, pos int) int {
	for i := 0; i < len(pattern); i++ {
		if text[pos+i] != pattern[i] {
			return i + 1
		}
	}
	return len(pattern)
}
