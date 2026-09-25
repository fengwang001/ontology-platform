// Package api 对外提供最长回文子串查询：New 构建，Longest 查询，SelfCheck 自检。
// 依赖 longest（进而依赖 manacher），不反向依赖。
package api

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"ontology/longest"
	"ontology/manacher"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrEmpty       = errors.New("api: empty string")
	ErrInvalidUTF8 = errors.New("api: invalid utf-8")
	ErrNotReady    = errors.New("api: Longest called before New")
	errSelfCheck   = errors.New("api: self check failed")
)

// InvalidUTF8Error 携带非法字节的偏移，可用 errors.As 取出。
type InvalidUTF8Error struct{ Offset int }

func (e *InvalidUTF8Error) Error() string {
	return fmt.Sprintf("%v at byte offset %d", ErrInvalidUTF8, e.Offset)
}

func (e *InvalidUTF8Error) Is(target error) bool { return target == ErrInvalidUTF8 }

// Checker 持有构建后的查询状态；零值未构建，Longest 返回 ErrNotReady。
type Checker struct {
	start, length int
	ready         bool
}

// New 校验并构建。任何校验失败都整体失败、不改变既有状态。
func (c *Checker) New(s string) error {
	if len(s) == 0 {
		return ErrEmpty
	}
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			i++
			continue
		}
		if r, size := utf8.DecodeRuneInString(s[i:]); r == utf8.RuneError && size == 1 {
			return &InvalidUTF8Error{Offset: i}
		} else {
			i += size
		}
	}
	d1, d2 := manacher.Compute([]rune(s))
	start, length := longest.Find(d1, d2)
	c.start, c.length, c.ready = start, length, true // 校验全过后一次性赋值
	return nil
}

// Longest 返回最长回文子串的起始与长度；未构建时返回 ErrNotReady。
func (c *Checker) Longest() (Start, Length int, err error) {
	if !c.ready {
		return 0, 0, ErrNotReady
	}
	return c.start, c.length, nil
}

// SelfCheck 对一组内置字符串核验四条不变量，全部通过返回 nil。
func (c *Checker) SelfCheck() error {
	for _, s := range []string{"abbaxyzabba", "a", "ab", "aa", "aba", "abba", "aabbaa", "上海自来水来自海上", "abaxabaxabb"} {
		r := []rune(s)
		d1, d2 := manacher.Compute(r)
		if !radiiSelfConsistent(r, d1, d2) { // 不变量 2：半径自洽
			return fmt.Errorf("%w: radii %q", errSelfCheck, s)
		}
		gs, gl := longest.Find(d1, d2)
		ns, nl := naive(r) // 不变量 1、3：与朴素一致（朴素即最长且最左）
		if gs != ns || gl != nl {
			return fmt.Errorf("%w: %q got (%d,%d) want (%d,%d)", errSelfCheck, s, gs, gl, ns, nl)
		}
	}
	// 不变量 4：失败不留痕
	var t Checker
	if err := t.New(""); !errors.Is(err, ErrEmpty) {
		return fmt.Errorf("%w: empty not rejected", errSelfCheck)
	}
	if err := t.New(string([]byte{'a', 0xff})); !errors.Is(err, ErrInvalidUTF8) {
		return fmt.Errorf("%w: invalid utf-8 not rejected", errSelfCheck)
	}
	if _, _, err := t.Longest(); !errors.Is(err, ErrNotReady) { // 被拒后状态不变
		return fmt.Errorf("%w: state mutated by rejection", errSelfCheck)
	}
	return nil
}

// naive 朴素参照：枚举每个中心（奇偶两类）向两侧逐字符扩展，严格大于才更新。
func naive(s []rune) (start, length int) {
	start, length = 0, -1
	for i := range s {
		k := 0
		for i-k >= 0 && i+k < len(s) && s[i-k] == s[i+k] {
			k++
		}
		if l := 2*k - 1; l > length {
			start, length = i-k+1, l
		}
	}
	for i := 1; i <= len(s); i++ {
		k := 0
		for i-k-1 >= 0 && i+k < len(s) && s[i-k-1] == s[i+k] {
			k++
		}
		if l := 2 * k; l > length {
			start, length = i-k, l
		}
	}
	return start, length
}

// radiiSelfConsistent 逐中心直接扩展核验 d1/d2，并核验对应最长子串确为回文。
func radiiSelfConsistent(s []rune, d1, d2 []int) bool {
	expand := func(lo, hi int) int { // 从最内对 (lo,hi) 向两侧扩，返回回文个数
		k := 0
		for lo-k >= 0 && hi+k < len(s) && s[lo-k] == s[hi+k] {
			k++
		}
		return k
	}
	for i := range s {
		if d1[i] != expand(i, i) || !isPal(s[i-d1[i]+1:i+d1[i]]) {
			return false
		}
	}
	for i := 0; i <= len(s); i++ {
		if d2[i] != expand(i-1, i) || (d2[i] > 0 && !isPal(s[i-d2[i]:i+d2[i]])) {
			return false
		}
	}
	return true
}

func isPal(s []rune) bool {
	for i := 0; i < len(s)/2; i++ {
		if s[i] != s[len(s)-1-i] {
			return false
		}
	}
	return true
}
