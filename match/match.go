// Package match 用 Rabin-Karp 滚动哈希定位 pattern 的所有出现位置。
package match

import (
	"errors"
	"sync/atomic"

	"ontology/hash"
)

// ErrEmptyPattern 表示 pattern 为空。
var ErrEmptyPattern = errors.New("match: empty pattern")

// verifications 统计逐字符验证的字符总数（进程内、并发安全）。
var verifications atomic.Int64

// Verifications 返回自上次 ResetVerifications 以来逐字符验证的总字符数。
func Verifications() int64 { return verifications.Load() }

// ResetVerifications 清零验证计数器。
func ResetVerifications() { verifications.Store(0) }

// FindAll 返回 pattern 在 text 中所有出现位置（升序，含重叠）。
// pattern 长于 text 返回空切片；空 pattern 以 ErrEmptyPattern panic，
// 调用方可用 errors.Is(recover().(error), ErrEmptyPattern) 区分。
func FindAll(text, pattern string) []int {
	positions, err := find(text, pattern)
	if err != nil {
		panic(err)
	}
	return positions
}

func find(text, pattern string) ([]int, error) {
	m, n := len(pattern), len(text)
	positions := []int{}
	if m == 0 {
		return nil, ErrEmptyPattern
	}
	if m > n {
		return positions, nil
	}
	win, _ := hash.New(hash.Base, hash.Mod)
	pat, _ := hash.New(hash.Base, hash.Mod)
	win.SetLength(m)
	pat.SetLength(m)
	for i := 0; i < m; i++ {
		win.Append(text[i])
		pat.Append(pattern[i])
	}
	target := pat.Value()
	for i := 0; i+m <= n; i++ {
		if win.Value() == target && equalAt(text, i, pattern) {
			positions = append(positions, i)
		}
		if i+m < n {
			win.Shift(text[i], text[i+m])
		}
	}
	return positions, nil
}

// equalAt 逐字符验证 text[start:start+m] 与 pattern 是否全等，
// 每个被比较的字符计入 verifications。
func equalAt(text string, start int, pattern string) bool {
	for j := 0; j < len(pattern); j++ {
		verifications.Add(1)
		if text[start+j] != pattern[j] {
			return false
		}
	}
	return true
}
