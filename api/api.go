// Package api 是布隆过滤器的对外接口，依赖 bloom。
package api

import (
	"errors"
	"fmt"

	"ontology/bloom"
	"ontology/hashk"
)

// 对外可判定的哨兵错误，与 bloom 的一一对应、互不相同。
var (
	ErrInvalidParam = bloom.ErrInvalidParam
	ErrEmptyElement = bloom.ErrEmptyElement
	ErrCapacity     = bloom.ErrCapacity
	ErrSelfCheck    = errors.New("api: self-check failed")
)

// Filter 是固定 m 位、k 个哈希函数的布隆过滤器，只增查不删。
type Filter struct{ f *bloom.Filter }

// New 构造过滤器；m<1 或 k<1 或 maxAdds<0 时返回 ErrInvalidParam。
func New(m, k, maxAdds int) (*Filter, error) {
	f, err := bloom.New(m, k, maxAdds)
	if err != nil {
		return nil, err
	}
	return &Filter{f: f}, nil
}

// Add 把 x 的 k 个位置置 1；空元素或超 maxAdds 时整体失败、状态不变。
func (f *Filter) Add(x []byte) error { return f.f.Add(x) }

// Test 仅当 x 的 k 个位全为 1 才返回 true；空元素返回 ErrEmptyElement。
func (f *Filter) Test(x []byte) (bool, error) { return f.f.Test(x) }

// Count 返回已成功 Add 的元素个数。
func (f *Filter) Count() int { return f.f.Count() }

// Snapshot 返回位数组快照，供演示与自检使用。
func (f *Filter) Snapshot() []bool { return f.f.Snapshot() }

// SelfCheck 对一组内置元素序列核验四条不变量，全部通过返回 nil。
func (f *Filter) SelfCheck() error {
	const m, k = 64, 3
	elems := [][]byte{[]byte("alpha"), []byte("beta"), []byte("gamma"), []byte("delta")}
	probes := [][]byte{[]byte("alpha"), []byte("delta"), []byte("omega"), []byte("zeta"), []byte("eta")}
	g, err := New(m, k, len(elems))
	if err != nil {
		return fmt.Errorf("%w: new: %v", ErrSelfCheck, err)
	}
	shadow := make([]bool, m) // 朴素参照：逐元素展开 k 个位置再按位比对
	for _, e := range elems {
		if err := g.Add(e); err != nil {
			return fmt.Errorf("%w: add: %v", ErrSelfCheck, err)
		}
		for _, p := range hashk.Positions(e, m, k) {
			shadow[p] = true
		}
	}
	for _, e := range elems { // 不变量 1：无假阴性
		if ok, _ := g.Test(e); !ok {
			return fmt.Errorf("%w: false negative on %q", ErrSelfCheck, e)
		}
	}
	for _, p := range probes { // 不变量 2、3：与朴素参照逐元素一致，true 仅来自置位
		want := true
		for _, pos := range hashk.Positions(p, m, k) {
			want = want && shadow[pos]
		}
		got, err := g.Test(p)
		if err != nil || got != want {
			return fmt.Errorf("%w: probe %q got=%v want=%v", ErrSelfCheck, p, got, want)
		}
	}
	before, snap := g.Count(), g.Snapshot() // 不变量 4：失败不留痕
	if err := g.Add(nil); !errors.Is(err, ErrEmptyElement) {
		return fmt.Errorf("%w: empty add: %v", ErrSelfCheck, err)
	}
	if err := g.Add([]byte("extra")); !errors.Is(err, ErrCapacity) {
		return fmt.Errorf("%w: overflow add: %v", ErrSelfCheck, err)
	}
	if _, err := g.Test(nil); !errors.Is(err, ErrEmptyElement) {
		return fmt.Errorf("%w: empty test: %v", ErrSelfCheck, err)
	}
	after, snap2 := g.Count(), g.Snapshot()
	if before != after || !equalSnap(snap, snap2) {
		return fmt.Errorf("%w: rejected op mutated state", ErrSelfCheck)
	}
	return nil
}

func equalSnap(a, b []bool) bool {
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
