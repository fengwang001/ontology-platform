// Package api 是布谷鸟过滤器的对外门面。
package api

import (
	"errors"
	"fmt"

	"ontology/cuck"
	"ontology/hash"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrInvalidParams = hash.ErrInvalidParams
	ErrNegativeKey   = cuck.ErrNegativeKey
	ErrFull          = cuck.ErrFull
	ErrNotInserted   = cuck.ErrNotInserted
)

// Filter 是对外暴露的过滤器句柄。
type Filter struct{ inner *cuck.Filter }

// New 构造过滤器；参数非法返回 ErrInvalidParams。
func New(numBuckets, entriesPerBucket, maxKicks int) (*Filter, error) {
	f, err := cuck.New(numBuckets, entriesPerBucket, maxKicks)
	if err != nil {
		return nil, err
	}
	return &Filter{inner: f}, nil
}

// Insert 插入键 x；负键返回 ErrNegativeKey，过滤器满返回 ErrFull（已回滚）。
func (f *Filter) Insert(x int64) error { return f.inner.Insert(x) }

// Lookup 报告 x 是否可能在过滤器中；允许假阳性，绝无假阴性。
func (f *Filter) Lookup(x int64) bool { return f.inner.Lookup(x) }

// Delete 删除键 x；x 未插入返回 ErrNotInserted，负键返回 ErrNegativeKey。
func (f *Filter) Delete(x int64) error { return f.inner.Delete(x) }

// Buckets 返回桶数组快照（深拷贝），供演示核对。
func (f *Filter) Buckets() [][]uint8 { return f.inner.Buckets() }

// SelfCheck 对一组内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	// 不变量 1+2：随机序列下，插入未删除的键 Lookup 必为 true，且与精确集合一致。
	f, err := New(64, 4, 8)
	if err != nil {
		return err
	}
	live := map[int64]bool{}
	var seed uint64 = 12345
	rand := func(n int64) int64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int64(seed>>16) % n
	}
	for i := 0; i < 400; i++ {
		x := rand(200)
		if rand(3) == 0 && live[x] {
			if err := f.Delete(x); err != nil {
				return fmt.Errorf("selfcheck delete: %w", err)
			}
			delete(live, x)
		} else if !live[x] {
			if err := f.Insert(x); err == nil {
				live[x] = true
			} else if !errors.Is(err, ErrFull) {
				return fmt.Errorf("selfcheck insert: %w", err)
			}
		}
	}
	for x := int64(0); x < 200; x++ {
		if live[x] && !f.Lookup(x) {
			return errors.New("selfcheck: false negative")
		}
	}
	// 不变量 3：指纹总数 == 净插入数。
	n := 0
	for _, b := range f.Buckets() {
		n += len(b)
	}
	if n != len(live) {
		return errors.New("selfcheck: fingerprint count mismatch")
	}
	// 不变量 4：满回滚与被拒操作不留痕。
	g, _ := New(2, 1, 1)
	if err := g.Insert(0); err != nil { // b0=[1]
		return err
	}
	if err := g.Insert(2); err != nil { // b0 满→i2=0^o(3)=1? f(2)=3,o=1,i2=3&1=1 → b1=[3]
		return err
	}
	before := fmt.Sprint(g.Buckets())
	if err := g.Insert(4); !errors.Is(err, ErrFull) { // f(4)=5,i1=0,i2=1 双满，踢 1 次仍满
		return errors.New("selfcheck: want ErrFull")
	}
	if fmt.Sprint(g.Buckets()) != before {
		return errors.New("selfcheck: rollback failed")
	}
	if err := g.Delete(99); !errors.Is(err, ErrNotInserted) {
		return errors.New("selfcheck: want ErrNotInserted")
	}
	if err := g.Insert(-1); !errors.Is(err, ErrNegativeKey) {
		return errors.New("selfcheck: want ErrNegativeKey")
	}
	if _, err := New(3, 1, 1); !errors.Is(err, ErrInvalidParams) {
		return errors.New("selfcheck: want ErrInvalidParams")
	}
	if !g.Lookup(0) || !g.Lookup(2) {
		return errors.New("selfcheck: state corrupted after rejections")
	}
	return nil
}
