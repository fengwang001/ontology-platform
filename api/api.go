// Package api 是线段树 RMQ 的对外门面：先校验再委托 rmq，失败不留痕。
package api

import (
	"errors"

	"ontology/rmq"
	"ontology/seg"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrBadOrder   = errors.New("rmq: l > r")        // 区间顺序非法
	ErrOutOfRange = errors.New("rmq: 查询界越出 [0,n]")  // 查询界越界
	ErrBadIndex   = errors.New("rmq: 更新下标越出 [0,n)") // 更新下标越界
	ErrEmpty      = errors.New("rmq: 空数组")          // 空数组
)

// RMQ 对外句柄。
type RMQ struct {
	inner *rmq.RMQ
	arr   []int64 // 当前数组镜像，供 SelfCheck 逐条核对
}

// New 用 arr 建树；空数组报 ErrEmpty，且不产生任何状态。
func New(arr []int64) (*RMQ, error) {
	if len(arr) == 0 {
		return nil, ErrEmpty
	}
	cp := make([]int64, len(arr))
	copy(cp, arr)
	return &RMQ{inner: rmq.Build(cp), arr: cp}, nil
}

// Size 返回元素个数。
func (r *RMQ) Size() int { return r.inner.N() }

// Query 返回 [l, r) 的最小值；空区间返回 seg.Inf。
// l>r 报 ErrBadOrder，界越出 [0,n] 报 ErrOutOfRange；失败不改变任何状态。
func (r *RMQ) Query(l, rr int) (int64, error) {
	n := r.inner.N()
	if l > rr {
		return 0, ErrBadOrder
	}
	if l < 0 || rr > n {
		return 0, ErrOutOfRange
	}
	return r.inner.Query(l, rr), nil
}

// Update 把下标 i 改为 v；i 越界报 ErrBadIndex，失败不改变任何状态。
func (r *RMQ) Update(i int, v int64) error {
	if i < 0 || i >= r.inner.N() {
		return ErrBadIndex
	}
	r.inner.Update(i, v)
	r.arr[i] = v
	return nil
}

// SelfCheck 对内置数组与操作序列核验四条不变量，全部通过返回 nil。
func (r *RMQ) SelfCheck() error {
	cases := [][]int64{
		{5, 2, 8, 1, 9, 3, 7, 4},
		{1},
		{3, 3, 3},
		{9, -4, 7, 0, -8, 5, 6, 2, 1, -1, 4}, // n=11 非 2 的幂，含负数
	}
	for _, c := range cases {
		if err := selfCheckOne(c); err != nil {
			return err
		}
	}
	return nil
}

func selfCheckOne(arr []int64) error {
	q, err := New(arr)
	if err != nil {
		return err
	}
	n := q.Size()
	cur := make([]int64, n)
	copy(cur, arr)
	naive := func(l, rr int) int64 {
		m := seg.Inf
		for _, v := range cur[l:rr] {
			m = seg.Min(m, v)
		}
		return m
	}
	// 不变量 1+3：全部 (l,r) 与朴素扫描一致，空区间为 +Inf。
	for l := 0; l <= n; l++ {
		for rr := l; rr <= n; rr++ {
			got, err := q.Query(l, rr)
			if err != nil || got != naive(l, rr) {
				return errors.New("selfcheck: 与朴素参照不一致")
			}
		}
	}
	// 不变量 2：每步更新后全量 (l,r) 逐条核对。
	for i := 0; i < n; i++ {
		if err := q.Update(i, int64(7-i*3)); err != nil {
			return err
		}
		cur[i] = int64(7 - i*3)
		for l := 0; l <= n; l++ {
			for rr := l; rr <= n; rr++ {
				got, _ := q.Query(l, rr)
				if got != naive(l, rr) {
					return errors.New("selfcheck: 更新后不一致")
				}
			}
		}
	}
	// 不变量 4：被拒操作不改变状态。
	before, _ := q.Query(0, n)
	if _, err := q.Query(2, 1); !errors.Is(err, ErrBadOrder) {
		return errors.New("selfcheck: l>r 未报 ErrBadOrder")
	}
	if _, err := q.Query(0, n+1); !errors.Is(err, ErrOutOfRange) {
		return errors.New("selfcheck: 越界未报 ErrOutOfRange")
	}
	if err := q.Update(n, 0); !errors.Is(err, ErrBadIndex) {
		return errors.New("selfcheck: 下标越界未报 ErrBadIndex")
	}
	if _, err := New(nil); !errors.Is(err, ErrEmpty) {
		return errors.New("selfcheck: 空数组未报 ErrEmpty")
	}
	after, _ := q.Query(0, n)
	if before != after {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	return nil
}
