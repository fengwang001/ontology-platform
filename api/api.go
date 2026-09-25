// Package api 对外提供字符串的 Z 数组与最短周期查询，状态只在进程内存。
package api

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"ontology/period"
	"ontology/zfn"
)

var (
	ErrEmpty       = errors.New("api: empty string")
	ErrInvalidUTF8 = errors.New("api: invalid utf-8")
	ErrNotBuilt    = errors.New("api: query before successful New")
)

type InvalidUTF8Error struct{ Offset int } // 携带首个非法字节偏移

func (e *InvalidUTF8Error) Error() string {
	return fmt.Sprintf("api: invalid utf-8 at byte offset %d", e.Offset)
}
func (e *InvalidUTF8Error) Is(target error) bool { return target == ErrInvalidUTF8 }

type API struct {
	z *zfn.Z
}

// New 校验 s 并构建；空串/非法 UTF-8 在写状态前返回，接收者不变，被拒后仍可重建。
func (a *API) New(s string) error {
	if len(s) == 0 {
		return ErrEmpty
	}
	if off, ok := invalidOffset(s); !ok {
		return &InvalidUTF8Error{Offset: off}
	}
	a.z = zfn.New([]rune(s)) // 全部校验通过后才一次性提交状态。
	return nil
}

// Z 返回 Z 数组副本；New 成功之前返回 ErrNotBuilt。
func (a *API) Z() ([]int, error) {
	if a.z == nil {
		return nil, ErrNotBuilt
	}
	return a.z.Array(), nil
}

// Period 返回最短周期；New 成功之前返回 ErrNotBuilt。
func (a *API) Period() (int, error) {
	if a.z == nil {
		return 0, ErrNotBuilt
	}
	return period.Shortest(a.z), nil
}

// invalidOffset 返回 s 中首个非法 UTF-8 字节偏移；合法时 ok 为真。
func invalidOffset(s string) (off int, ok bool) {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return i, false
		}
		i += size
	}
	return 0, true
}

var selfCases = []string{"ababa", "aaaa", "abab", "abcabcabc", "aabaaaba", "αβαβα"}

// SelfCheck 对内置字符串核验四条不变量，全通过返回 nil。
func (a *API) SelfCheck() error {
	for _, s := range selfCases {
		rs := []rune(s)
		g := zfn.New(rs)
		if zz := g.Array(); !eqInt(zz, naiveZ(rs)) || zz[0] != 0 {
			return fmt.Errorf("selfcheck: Z mismatch for %q", s)
		}
		for i := 0; i < len(rs); i++ {
			if g.At(i) < 0 || g.At(i) > len(rs)-i {
				return fmt.Errorf("selfcheck: Z out of range for %q", s)
			}
		}
		if period.Shortest(g) != naivePeriod(rs) {
			return fmt.Errorf("selfcheck: period mismatch for %q", s)
		}
	}
	var b API // 失败不留痕：三类错误互不相同，拒绝后仍可重建。
	if err := b.New(""); !errors.Is(err, ErrEmpty) {
		return fmt.Errorf("selfcheck: empty error = %v", err)
	}
	var bad *InvalidUTF8Error
	if err := b.New("a" + string([]byte{0xff})); !errors.As(err, &bad) || bad.Offset != 1 {
		return fmt.Errorf("selfcheck: invalid-utf8 error = %v", err)
	}
	if _, err := b.Z(); !errors.Is(err, ErrNotBuilt) {
		return fmt.Errorf("selfcheck: not-built Z error = %v", err)
	}
	if _, err := b.Period(); !errors.Is(err, ErrNotBuilt) {
		return fmt.Errorf("selfcheck: not-built Period error = %v", err)
	}
	if err := b.New("ababa"); err != nil {
		return fmt.Errorf("selfcheck: rebuild after rejection = %v", err)
	}
	if zz, _ := b.Z(); !eqInt(zz, []int{0, 0, 3, 0, 1}) {
		return errors.New("selfcheck: ababa Z after rebuild")
	} else if p, _ := b.Period(); p != 2 {
		return errors.New("selfcheck: ababa period after rebuild")
	}
	return nil
}

func naiveZ(rs []rune) []int {
	z := make([]int, len(rs))
	for i := 1; i < len(rs); i++ {
		for i+z[i] < len(rs) && rs[z[i]] == rs[i+z[i]] {
			z[i]++
		}
	}
	return z
}

func naivePeriod(rs []rune) int {
	for p := 1; p < len(rs); p++ {
		ok := true
		for i := 0; i < len(rs)-p; i++ {
			if rs[i] != rs[i+p] {
				ok = false
				break
			}
		}
		if ok {
			return p
		}
	}
	return len(rs)
}

func eqInt(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
