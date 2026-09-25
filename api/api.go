// Package api 对外门面：Put/Delete/Lookup/Snapshot/SelfCheck。
package api

import (
	"fmt"
	"reflect"
	"sort"
	"sync"

	"ontology/pindex"
	"ontology/ptab"
)

// 对外暴露的哨兵错误，与 ptab 同源、互不相同。
var (
	ErrBadID    = ptab.ErrBadID
	ErrBadScore = ptab.ErrBadScore
	ErrNotFound = ptab.ErrNotFound
)

// API 是并发安全的对外入口：写操作互斥，只读操作可并发。
type API struct {
	mu  sync.RWMutex
	tab *ptab.Table
	idx *pindex.Index
}

// New 返回空实例。
func New() *API {
	ix := pindex.New()
	return &API{tab: ptab.New(ix), idx: ix}
}

// Put 幂等 upsert。
func (a *API) Put(id, key, score int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tab.Put(id, key, score)
}

// Delete 删除一行。
func (a *API) Delete(id int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tab.Delete(id)
}

// Lookup 返回 key 下命中谓词的行 ID 升序列表。
func (a *API) Lookup(key int) []int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.idx.Lookup(key)
}

// Snapshot 返回全表行副本。
func (a *API) Snapshot() map[int]ptab.Row {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tab.Snapshot()
}

// dump 导出当前索引全貌（Key → ID 升序）。调用方需持锁或单线程。
func (a *API) dump() map[int][]int {
	out := map[int][]int{}
	for _, k := range a.idx.Keys() {
		out[k] = a.idx.Lookup(k)
	}
	return out
}

// checkRebuild 核验不变量 1/2/3：索引 == 扫全表只留命中行按 Key 分组的重建结果。
func (a *API) checkRebuild() error {
	want := map[int][]int{}
	for id, r := range a.tab.Snapshot() {
		if pindex.Hit(r.Score) {
			want[r.Key] = append(want[r.Key], id)
		}
	}
	for k := range want {
		sort.Ints(want[k])
	}
	if got := a.dump(); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("api: index %v != rebuild %v", got, want)
	}
	return nil
}

// SelfCheck 核验四条不变量：当前状态与全量重建一致，并跑内置操作序列。
func (a *API) SelfCheck() error {
	a.mu.RLock()
	err := a.checkRebuild()
	a.mu.RUnlock()
	if err != nil {
		return err
	}
	return builtinChecks()
}

// builtinChecks 在内置操作序列上核验四条不变量（不变量 4 失败不留痕在此钉住）。
func builtinChecks() error {
	// 第三节六步序列：逐步比对索引内容。
	six := New()
	want := []map[int][]int{
		{10: {1}}, {10: {1}}, {10: {1}, 20: {3}},
		{20: {3}}, {10: {2}, 20: {3}}, {10: {2, 3}},
	}
	ops := [][3]int{{1, 10, 80}, {2, 10, 30}, {3, 20, 60}, {1, 10, 49}, {2, 10, 50}, {3, 10, 70}}
	for i, op := range ops {
		if err := six.Put(op[0], op[1], op[2]); err != nil {
			return err
		}
		if got := six.dump(); !reflect.DeepEqual(got, want[i]) {
			return fmt.Errorf("api: step %d index %v != %v", i+1, got, want[i])
		}
	}
	// 不变量 4：三类被拒操作互不相同且不留痕，之后仍可正常使用。
	if e1, e2, e3 := six.Put(-1, 1, 1), six.Put(9, 1, 101), six.Delete(99); //nolint
	e1 != ErrBadID || e2 != ErrBadScore || e3 != ErrNotFound {
		return fmt.Errorf("api: sentinel errors %v %v %v", e1, e2, e3)
	}
	if got := six.dump(); !reflect.DeepEqual(got, want[5]) {
		return fmt.Errorf("api: rejected ops changed state: %v", got)
	}
	if err := six.Put(9, 9, 90); err != nil {
		return fmt.Errorf("api: unusable after rejection: %w", err)
	}
	// 确定性混合序列：每步之后都与全量重建比对。
	mix := New()
	for i := 0; i < 300; i++ {
		if err := mix.Put(i%37, i%11, (i*37+i/7)%101); err != nil {
			return err
		}
		if i%3 == 0 {
			_ = mix.Delete(i%37 - 1)
		}
		if err := mix.checkRebuild(); err != nil {
			return fmt.Errorf("api: seq step %d: %w", i, err)
		}
	}
	return nil
}
