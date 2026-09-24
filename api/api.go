// Package api 是 LSN 空洞检测与压缩重编号的对外门面；只依赖 cmp，状态仅存进程内存。
package api

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/cmp"
	"ontology/lsn"
)

// Engine 线程安全：写互斥；FindNew/FindOld/Holes/Count/SelfCheck 可并发只读。
type Engine struct {
	mu   sync.RWMutex
	set  *lsn.Set
	map_ *cmp.Mapper
}

func New() *Engine { // 返回空引擎。
	s := lsn.New()
	return &Engine{set: s, map_: cmp.NewMap(s)}
}

func (e *Engine) Append(v int64) error { // 加入一条 LSN；负数、重复被整体拒绝，状态不变。
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.set.Add(v)
}

func (e *Engine) FindNew(old int64) (int64, error) { // 旧 LSN 的新号（0 起名次）；未知返回 cmp.ErrUnknownLSN。
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.map_.FindNew(old)
}

func (e *Engine) FindOld(newIdx int64) (int64, error) { // FindNew 的逆；越界返回 cmp.ErrNewIndexOutOfRange。
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.map_.FindOld(newIdx)
}

func (e *Engine) Holes() []int64 { // 返回 [min,max] 内缺失整数的升序副本（lsn.Holes 每次新建切片）。
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.set.Holes()
}

func (e *Engine) Count() int { // 返回当前记录数。
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.set.Len()
}

// sorted 借 FindOld 遍历 0..n-1 得严格升序旧 LSN；仅用于无并发的自检临时实例。
func (e *Engine) sorted() []int64 {
	out := make([]int64, e.set.Len())
	for k := range out {
		out[k], _ = e.map_.FindOld(int64(k))
	}
	return out
}

// SelfCheck 对内置八步序列核验第二节四条不变量；在临时实例运行，不改接收者。
func (e *Engine) SelfCheck() error {
	tmp := New()
	seq := []int64{10, 13, 15, 12, 17, 10, 20, 18}
	want := [][]int64{{}, {11, 12}, {11, 12, 14}, {11, 14}, {11, 14, 16},
		{11, 14, 16}, {11, 14, 16, 18, 19}, {11, 14, 16, 19}}
	var present []int64
	for i, v := range seq {
		err := tmp.Append(v)
		switch {
		case i == 5 && !errors.Is(err, lsn.ErrDuplicate):
			return fmt.Errorf("step6: %v", err)
		case i != 5 && err != nil:
			return fmt.Errorf("step%d: %v", i+1, err)
		}
		if err == nil {
			present = append(present, v)
		}
		if !equal(tmp.Holes(), want[i]) {
			return fmt.Errorf("step%d holes mismatch", i+1)
		}
	}
	return verify(tmp, present)
}

// verify 对拍批量 oracle（排序赋 0..n-1 + [min,max] 枚举缺失），核验四不变量。
func verify(e *Engine, appended []int64) error {
	o := append([]int64(nil), appended...)
	sort.Slice(o, func(i, j int) bool { return o[i] < o[j] })
	n := len(o)
	if e.Count() != n || !equal(e.sorted(), o) {
		return errors.New("batch consistency: set/count mismatch") // 不变量 1
	}
	for k, old := range o { // 不变量 1+2：名次即新号，两个方向互逆
		if nk, err := e.FindNew(old); err != nil || nk != int64(k) {
			return fmt.Errorf("FindNew(%d)=(%d,%v) want %d", old, nk, err, k)
		}
		if back, err := e.FindOld(int64(k)); err != nil || back != old {
			return fmt.Errorf("FindOld(%d)=(%d,%v) want %d", k, back, err, old)
		}
	}
	wh := []int64{} // 不变量 1 空洞部分 + 不变量 3
	for i := 1; i < n; i++ {
		for v := o[i-1] + 1; v < o[i]; v++ {
			wh = append(wh, v)
		}
	}
	if !equal(e.Holes(), wh) {
		return errors.New("holes mismatch batch oracle")
	}
	if n >= 2 {
		if c := o[n-1] - o[0] + 1 - int64(n); int64(len(wh)) != c || c < 0 {
			return errors.New("hole conservation broken")
		}
	}
	// 不变量 4：四类拒绝（哨兵互异由 api 测试 TestRejectedOpsNoTrace 钉住）不留痕。
	b4, bh, bn := e.sorted(), e.Holes(), e.Count()
	bad := []func() error{
		func() error { return e.Append(-1) }, func() error { return e.Append(10) },
		func() error { _, err := e.FindNew(999); return err },
		func() error { _, err := e.FindOld(-1); return err },
		func() error { _, err := e.FindOld(int64(n)); return err },
	}
	for i, f := range bad {
		if err := f(); err == nil {
			return fmt.Errorf("reject %d: unexpected success", i)
		}
		if !equal(e.sorted(), b4) || !equal(e.Holes(), bh) || e.Count() != bn {
			return fmt.Errorf("state changed by rejected op %d", i)
		}
	}
	return nil
}

func equal(a, b []int64) bool {
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
