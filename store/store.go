// Package store 保存记录、归属主体与法律保全，并按主体维护种子索引。
package store

import (
	"errors"
	"sort"
)

// 规范错误集合：三个包与 erase 门面共用同一组哨兵错误。
var (
	ErrInvalid       = errors.New("store: invalid argument")
	ErrClockRollback = errors.New("store: clock moved backwards")
	ErrExists        = errors.New("store: record already exists")
	ErrNotFound      = errors.New("store: record not found")
)

// Rec 是记录的只读视图。
type Rec struct {
	ID     int64
	Owners []string
	Anon   bool
}

// Store 是记录存储；自身不加锁，由 erase.Executor 统一加锁。
type Store struct {
	recs     map[int64]*Rec
	subjects map[string]map[int64]struct{}
	holds    map[int64]int64
}

// New 创建空存储。
func New() *Store {
	return &Store{
		recs:     map[int64]*Rec{},
		subjects: map[string]map[int64]struct{}{},
		holds:    map[int64]int64{},
	}
}

// Put 登记一条记录。调用方（erase.Executor）负责参数、时钟与墓碑校验，
// 以及重复登记判断；此处假定 id 不存在、owners 已去重且非空。
func (st *Store) Put(id int64, owners []string) error {
	owned := make([]string, len(owners))
	copy(owned, owners)
	r := &Rec{ID: id, Owners: owned}
	st.recs[id] = r
	for _, s := range owned {
		set := st.subjects[s]
		if set == nil {
			set = map[int64]struct{}{}
			st.subjects[s] = set
		}
		set[id] = struct{}{}
	}
	return nil
}

// Get 返回记录只读视图。
// 返回的切片为内部状态，调用方只读且不得修改。
func (st *Store) Get(id int64) (*Rec, bool) {
	r, ok := st.recs[id]
	return r, ok
}

// Seeds 返回归属含 s 的全部记录 id（升序）。
func (st *Store) Seeds(s string) []int64 {
	ids := make([]int64, 0, len(st.subjects[s]))
	for id := range st.subjects[s] {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// RemoveOwner 从记录的 owners 中移除 s。
// 假定记录存在；s 不在 owners 中时为空操作。
func (st *Store) RemoveOwner(id int64, s string) {
	r := st.recs[id]
	kept := r.Owners[:0]
	removed := false
	for _, o := range r.Owners {
		if o == s {
			removed = true
			continue
		}
		kept = append(kept, o)
	}
	if !removed {
		return
	}
	r.Owners = kept
	delete(st.subjects[s], id)
	if len(st.subjects[s]) == 0 {
		delete(st.subjects, s)
	}
}

// Anonymize 清空 owners 并打匿名标记。
func (st *Store) Anonymize(id int64) {
	r := st.recs[id]
	for _, o := range r.Owners {
		delete(st.subjects[o], id)
		if len(st.subjects[o]) == 0 {
			delete(st.subjects, o)
		}
	}
	r.Owners = nil
	r.Anon = true
}

// Delete 删除记录及其主体索引与保全。
func (st *Store) Delete(id int64) {
	r, ok := st.recs[id]
	if !ok {
		return
	}
	for _, o := range r.Owners {
		delete(st.subjects[o], id)
		if len(st.subjects[o]) == 0 {
			delete(st.subjects, o)
		}
	}
	delete(st.recs, id)
	delete(st.holds, id)
}

// Hold 覆盖记录的保全截止时刻。
func (st *Store) Hold(id int64, until int64) { st.holds[id] = until }

// Held 报告记录在 now 是否处于保全中（t < until 生效，恰等失效）。
func (st *Store) Held(id int64, now int64) bool {
	until, ok := st.holds[id]
	return ok && now < until
}

// Count 返回记录数（供测试与朴素模拟使用）。
func (st *Store) Count() int { return len(st.recs) }

// IDs 返回全部记录 id（升序，供朴素模拟使用）。
func (st *Store) IDs() []int64 {
	ids := make([]int64, 0, len(st.recs))
	for id := range st.recs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
