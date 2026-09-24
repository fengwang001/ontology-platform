// Package api 是布谷鸟过滤器的对外封装：构造、操作包装与自检。依赖 cuck。
package api

import (
	"errors"
	"fmt"

	"ontology/cuck"
	"ontology/hash"
)

// 可判定哨兵错误（与底层同一实例，errors.Is 可用）。
var (
	ErrInvalidParams = hash.ErrInvalidParams // 构造参数非法
	ErrNegativeKey   = cuck.ErrNegativeKey   // 负键
	ErrFull          = cuck.ErrFull          // 过滤器满（已回滚）
	ErrNotInserted   = cuck.ErrNotInserted   // 删除未插入键
)

// Filter 是对外句柄。
type Filter struct{ c *cuck.Filter }

// New 构造过滤器；参数非法返回 ErrInvalidParams。
func New(numBuckets, entriesPerBucket, maxKicks int) (*Filter, error) {
	c, err := cuck.New(numBuckets, entriesPerBucket, maxKicks)
	if err != nil {
		return nil, err
	}
	return &Filter{c: c}, nil
}

// Insert 插入非负键 x。
func (f *Filter) Insert(x int64) error { return f.c.Insert(x) }

// Lookup 报告 x 是否可能存在（允许假阳性，绝无假阴性）。
func (f *Filter) Lookup(x int64) bool { return f.c.Lookup(x) }

// Delete 删除已插入的键 x。
func (f *Filter) Delete(x int64) error { return f.c.Delete(x) }

// Count 返回净指纹数（成功插入数 − 成功删除数）。
func (f *Filter) Count() int { return f.c.Count() }

// Buckets 返回桶数组深拷贝（0 为空槽）。
func (f *Filter) Buckets() [][]int { return f.c.Buckets() }

// naive 朴素扫描 i1/i2 两桶找指纹，作为不变量 2 的参照。
func naive(bs [][]int, numBuckets int, x int64) bool {
	fp := hash.Fingerprint(x)
	for _, b := range []int{hash.I1(x, numBuckets), hash.I2(x, numBuckets)} {
		for _, v := range bs[b] {
			if v == fp {
				return true
			}
		}
	}
	return false
}

// SelfCheck 对一组内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	f, err := New(64, 4, 8)
	if err != nil {
		return err
	}
	present := map[int64]bool{}
	net := 0
	for x := int64(0); x < 100; x++ { // 内置序列：插入 0..99
		if err := f.Insert(x); err != nil {
			return fmt.Errorf("selfcheck insert: %w", err)
		}
		present[x], net = true, net+1
	}
	for x := int64(0); x < 100; x += 3 { // 删除 3 的倍数
		if err := f.Delete(x); err != nil {
			return fmt.Errorf("selfcheck delete: %w", err)
		}
		present[x], net = false, net-1
	}
	for x := int64(0); x < 100; x++ { // 不变量 1：无假阴性
		if present[x] && !f.Lookup(x) {
			return fmt.Errorf("selfcheck: false negative on %d", x)
		}
	}
	bs, nb := f.Buckets(), 64
	for x := int64(0); x < 300; x++ { // 不变量 2：成员与存储一致
		if f.Lookup(x) != naive(bs, nb, x) {
			return fmt.Errorf("selfcheck: lookup/storage mismatch on %d", x)
		}
	}
	total := 0 // 不变量 3：指纹守恒
	for _, b := range bs {
		for _, v := range b {
			if v != 0 {
				total++
			}
		}
	}
	if total != net || f.Count() != net {
		return errors.New("selfcheck: fingerprint count mismatch")
	}
	snap, cnt := f.Buckets(), f.Count() // 不变量 4：失败不留痕
	if f.Insert(-1) == nil || f.Delete(1000) == nil {
		return errors.New("selfcheck: rejected op not rejected")
	}
	tiny, _ := New(4, 1, 1)
	for _, x := range []int64{1, 5, 2, 4} {
		tiny.Insert(x)
	}
	tsnap := tiny.Buckets()
	if tiny.Insert(8) == nil {
		return errors.New("selfcheck: full filter not rejected")
	}
	if !equalBuckets(f.Buckets(), snap) || f.Count() != cnt ||
		!equalBuckets(tiny.Buckets(), tsnap) {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}

func equalBuckets(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
