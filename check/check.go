// Package check 提供朴素参照实现与用于演示碰撞假阳性的错误实现。
package check

import (
	"errors"

	"ontology/hash"
	"ontology/match"
)

// ErrEmptyPattern 透传 match 包的哨兵错误，保证 errors.Is 可区分。
var ErrEmptyPattern = match.ErrEmptyPattern

// NaiveFindAll 逐位置做 substring 比较，作为朴素参照（含重叠出现）。
func NaiveFindAll(text, pattern string) ([]int, error) {
	m := len(pattern)
	if m == 0 {
		return nil, ErrEmptyPattern
	}
	if m > len(text) {
		return nil, nil
	}
	var hits []int
	for start := 0; start <= len(text)-m; start++ {
		if text[start:start+m] == pattern {
			hits = append(hits, start)
		}
	}
	return hits, nil
}

// BuggyFindAll 是“哈希命中即判成功”的错误实现：不做逐字符验证。
// 仅用于在碰撞用例上钉住假阳性现象。
func BuggyFindAll(text, pattern string) ([]int, error) {
	m := len(pattern)
	if m == 0 {
		return nil, errors.New("check: empty pattern")
	}
	n := len(text)
	if m > n {
		return nil, nil
	}
	pow, err := hash.Power(match.Base, match.Mod, uint64(m)-1)
	if err != nil {
		return nil, err
	}
	ph, _ := hash.New(match.Base, match.Mod)
	wh, _ := hash.New(match.Base, match.Mod)
	for i := 0; i < m; i++ {
		ph.Append(pattern[i])
		wh.Append(text[i])
	}
	var hits []int
	for start := 0; start <= n-m; start++ {
		if wh.Value() == ph.Value() {
			hits = append(hits, start)
		}
		if start < n-m {
			if err := wh.Slide(text[start], text[start+m], pow); err != nil {
				return nil, err
			}
		}
	}
	return hits, nil
}
