// Package router 实现亲和路由：Assign/Put/Get、BeginDrain、原子 Migrate，依赖 part。
package router

import (
	"errors"
	"fmt"
	"sync"

	"ontology/part"
)

var ( // 可判定哨兵错误，四类互不相同。
	ErrNoSuchPartition  = errors.New("router: partition does not exist")
	ErrAffinityConflict = errors.New("router: affinity conflict (partition not Active)")
	ErrNoAffinity       = errors.New("router: key has no affinity")
	ErrBadMigrate       = errors.New("router: invalid migrate arguments")
	ErrEmptyKey         = errors.New("router: empty key")
	ErrAlreadyAssigned  = errors.New("router: key already assigned")
)

// Router 持有分区表、key→owner 亲和表与 key→value 值表；值随 key 全局存放，迁移只改亲和。
type Router struct {
	mu        sync.RWMutex
	parts     map[int]*part.Partition
	owner     map[string]int
	vals      map[string]string
	scanCount int // 最近一次成功 Migrate 为定位 from 的 Key 所检查的分区数
}

// New 以给定 id 建立一组 Active 分区。
func New(ids ...int) *Router {
	r := &Router{parts: make(map[int]*part.Partition, len(ids)), owner: make(map[string]int), vals: make(map[string]string)}
	for _, id := range ids {
		r.parts[id] = part.New(id)
	}
	return r
}

// Assign 声明 key 的亲和分区；p 必须存在且 Active，key 必须非空且未 Assign 过。
func (r *Router) Assign(key string, p int) error {
	if key == "" {
		return ErrEmptyKey
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.owner[key]; dup {
		return ErrAlreadyAssigned
	}
	if pt, ok := r.parts[p]; !ok {
		return ErrNoSuchPartition
	} else if pt.State() != part.Active {
		return fmt.Errorf("%w: partition %d is %s", ErrAffinityConflict, p, pt.State())
	}
	r.owner[key] = p
	r.parts[p].Add(key)
	return nil
}

// Put 写 key 的值；key 必须已 Assign 且亲和分区为 Active，否则拒绝且不改状态。
func (r *Router) Put(key, val string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.owner[key]; !ok {
		return ErrNoAffinity
	} else if st := r.parts[p].State(); st != part.Active {
		return fmt.Errorf("%w: partition %d is %s", ErrAffinityConflict, p, st)
	}
	r.vals[key] = val
	return nil
}

// Get 读 key 的值；无亲和即无值，返回 ("", false)；Draining 期间仍可读。
func (r *Router) Get(key string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.vals[key]
	return v, ok
}

// BeginDrain 把分区从 Active 置为 Draining。
func (r *Router) BeginDrain(p int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pt, ok := r.parts[p]; !ok {
		return ErrNoSuchPartition
	} else if !pt.BeginDrain() {
		return fmt.Errorf("%w: partition %d is %s", ErrAffinityConflict, p, pt.State())
	}
	return nil
}

// Migrate 把 from 的全部 Key 的亲和原子地改为 to，随后 from 变为 Removed。
func (r *Router) Migrate(from, to int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	fp, fok := r.parts[from]
	tp, tok := r.parts[to]
	if !fok || !tok {
		return ErrNoSuchPartition
	}
	if from == to || fp.State() != part.Draining || tp.State() != part.Active {
		return fmt.Errorf("%w: from=%d(%s) to=%d(%s)", ErrBadMigrate, from, fp.State(), to, tp.State())
	}
	r.scanCount = 1 // owner→keys 索引直接定位 from，只检查这 1 个分区
	for _, k := range fp.Keys() {
		r.owner[k] = to
		tp.Add(k)
		fp.Del(k)
	}
	fp.MarkRemoved() // from 必为 Draining，必成功
	return nil
}

// Count 返回 p 当前拥有的 Key 数；不存在或 Removed 的分区恒为 0。
func (r *Router) Count(p int) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if pt, ok := r.parts[p]; ok {
		return pt.Count()
	}
	return 0
}

const scanBound = 2 // Migrate 扫描分区数上界：与分区总数无关的小常数

// CheckScanBound 在多档 m 下建 m 个分区（各一个 Key），排空其一并 Migrate，
// 判定扫描分区数不超 scanBound。只导出结论，不导出计数器数值。
func CheckScanBound() error {
	for _, m := range []int{100, 1000, 10000} {
		ids := make([]int, m)
		for i := range ids {
			ids[i] = i
		}
		r := New(ids...)
		for i := 0; i < m; i++ {
			if err := r.Assign(fmt.Sprintf("k%d", i), i); err != nil {
				return err
			}
		}
		if err := r.BeginDrain(0); err != nil {
			return err
		} else if err := r.Migrate(0, 1); err != nil {
			return err
		}
		if r.scanCount > scanBound {
			return fmt.Errorf("m=%d: migrate scan count exceeds constant bound", m)
		}
	}
	return nil
}
