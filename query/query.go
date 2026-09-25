// Package query 在 rhash 之上提供子串判等（O(1)）与后缀最长公共前缀
// （O(log n) 二分）两种查询。依赖 rhash，构造后只读、可并发。
package query

import (
	"errors"
	"math/bits"

	"ontology/rhash"
)

// 可判定的哨兵错误；api 层另加 ErrEmptyInput，三者互不相同。
var (
	// ErrNotBuilt：查询器尚未成功构建（零值或 nil）即被调用。
	ErrNotBuilt = errors.New("query: 查询器尚未构建")
	// ErrOutOfRange：端点落在 [0,n] 之外，或 l>r。
	ErrOutOfRange = errors.New("query: 区间或下标越界")
)

// Query 包装一个 rhash.Hasher。
type Query struct {
	h *rhash.Hasher
}

// New 基于已构建的哈希表构造查询器。
func New(h *rhash.Hasher) *Query { return &Query{h: h} }

func (q *Query) checkRange(l, r int) error {
	n := q.h.Len()
	if l < 0 || r < 0 || l > n || r > n || l > r {
		return ErrOutOfRange
	}
	return nil
}

// Equal 报告 s[l1..r1) 与 s[l2..r2) 是否逐字节相等。
// 长度不等立即 false；长度相等时两个模的哈希同时相等才相等。
func (q *Query) Equal(l1, r1, l2, r2 int) (bool, error) {
	if q == nil || q.h == nil {
		return false, ErrNotBuilt
	}
	if err := q.checkRange(l1, r1); err != nil {
		return false, err
	}
	if err := q.checkRange(l2, r2); err != nil {
		return false, err
	}
	if r1-l1 != r2-l2 {
		return false, nil
	}
	a1, a2 := q.h.Hash(l1, r1)
	b1, b2 := q.h.Hash(l2, r2)
	return a1 == b1 && a2 == b2, nil
}

// lcp 是 LCP 的内部形式，额外返回二分过程中调用 Equal 的次数；
// 不修改任何字段，因此并发调用无竞争。
func (q *Query) lcp(i, j int) (int, int, error) {
	if q == nil || q.h == nil {
		return 0, 0, ErrNotBuilt
	}
	n := q.h.Len()
	if i < 0 || j < 0 || i > n || j > n {
		return 0, 0, ErrOutOfRange
	}
	maxK := n - i
	if n-j < maxK {
		maxK = n - j
	}
	// 在 [0,maxK] 上找上中位数二分：可行长度上界。
	lo, hi, cmps := 0, maxK, 0
	for lo < hi {
		mid := (lo + hi + 1) / 2
		cmps++
		eq, err := q.Equal(i, i+mid, j, j+mid)
		if err != nil {
			return 0, cmps, err
		}
		if eq {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo, cmps, nil
}

// LCP 返回后缀 s[i:] 与 s[j:] 的最长公共前缀长度。
// i、j 允许等于 n（空前缀），结果为 0。
func (q *Query) LCP(i, j int) (int, error) {
	k, _, err := q.lcp(i, j)
	return k, err
}

// SelfCheck 委托底层哈希自检，并核验任意 LCP 的哈希比较次数不超过
// ceil(log2 n)+1（仅给成败，不暴露非导出的比较次数数值）。
func (q *Query) SelfCheck() error {
	if q == nil || q.h == nil {
		return ErrNotBuilt
	}
	if err := q.h.SelfCheck(); err != nil {
		return err
	}
	n := q.h.Len()
	limit := bits.Len(uint(n)) + 1 // ceil(log2 n)+1；New 拒绝空串，故 n>=1
	// 只核验固定内置对，保持自检 O(1)；大规模上界由测试循环钉住。
	pairs := [][2]int{{0, 0}, {0, n}, {n, n}, {0, n / 2}, {n / 3, 2 * n / 3}}
	for _, p := range pairs {
		if p[0] > n || p[1] > n {
			continue
		}
		if _, cmps, err := q.lcp(p[0], p[1]); err != nil || cmps > limit {
			return errors.New("query: LCP 比较次数超过 ceil(log2 n)+1")
		}
	}
	return nil
}
