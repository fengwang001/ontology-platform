// Package api 对外提供快照+增量双读存储。依赖 read。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/read"
	"ontology/snap"
)

// 三类可判定、互不相同的哨兵错误。
var (
	ErrEmptyKey   = errors.New("api: empty key")
	ErrEmptyVal   = errors.New("api: empty val")
	ErrBeforeSnap = read.ErrBeforeSnap
	ErrSelfCheck  = errors.New("api: selfcheck failed")
)

// Store 是进程内存中的快照+增量存储，并发安全。
type Store struct {
	mu      sync.RWMutex
	log     snap.Log
	base    map[string]string
	snapSeq int64
}

// New 返回一个空 Store。
func New() *Store { return &Store{base: map[string]string{}} }

// Put 追加一条 Put 日志；key、val 均非空，否则整体失败、状态不变。
func (s *Store) Put(key, val string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if val == "" {
		return ErrEmptyVal
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Append(key, snap.Put, val)
	return nil
}

// Del 追加一条 Del 日志；key 非空，否则整体失败、状态不变。
func (s *Store) Del(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Append(key, snap.Del, "")
	return nil
}

// Snapshot 冻结当前状态为不可变快照；日志保留，读结果不变。
func (s *Store) Snapshot() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.base, s.snapSeq = snap.Build(s.log.Entries(), s.log.Seq())
}

// Read 返回 key 在 atSeq 时刻的值；atSeq < snapSeq 时拒绝且状态不变。
func (s *Store) Read(key string, atSeq int64) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return read.ReadAt(s.log.Entries(), s.base, s.snapSeq, atSeq, key)
}

// batch 是不变量 1 的参照：从空出发按 Seq 升序应用 Seq<=atSeq 的全部日志。
func batch(entries []snap.Entry, atSeq int64, key string) (string, bool) {
	val, ok := "", false
	for _, e := range entries {
		if e.Seq > atSeq {
			break
		}
		if e.Key != key {
			continue
		}
		if e.Op == snap.Put {
			val, ok = e.Val, true
		} else {
			val, ok = "", false
		}
	}
	return val, ok
}

// SelfCheck 对一组内置事件序列核验四条不变量，可被并发调用。
func (s *Store) SelfCheck() error {
	t := New()
	type ev struct{ op, k, v string }
	for _, e := range []ev{
		{"put", "a", "1"}, {"put", "b", "2"}, {"snap", "", ""},
		{"put", "a", "5"}, {"del", "b", ""}, {"put", "c", "9"},
		{"snap", "", ""}, {"put", "a", "7"}, {"del", "a", ""}, {"put", "b", "3"},
	} {
		switch e.op {
		case "put":
			t.Put(e.k, e.v)
		case "del":
			t.Del(e.k)
		case "snap":
			t.Snapshot()
		}
	}
	entries := t.log.Entries()
	keys := []string{"a", "b", "c"}
	// 不变量 1+2：任意 atSeq>=snapSeq 逐键等于批量重算；交叠键增量须胜出。
	for _, k := range keys {
		for at := t.snapSeq; at <= t.log.Seq(); at++ {
			got, ok, err := t.Read(k, at)
			if err != nil {
				return fmt.Errorf("%w: read: %v", ErrSelfCheck, err)
			}
			if w, wok := batch(entries, at, k); got != w || ok != wok {
				return fmt.Errorf("%w: inv1 %q@%d", ErrSelfCheck, k, at)
			}
		}
	}
	// 不变量 3：快照前后同一 atSeq 的读结果完全一致。
	at := t.log.Seq()
	before := make(map[string]bool)
	for _, k := range keys {
		_, before[k], _ = t.Read(k, at)
	}
	t.Snapshot()
	for _, k := range keys {
		if _, ok, _ := t.Read(k, at); ok != before[k] {
			return fmt.Errorf("%w: inv3 %q", ErrSelfCheck, k)
		}
	}
	// 不变量 4：被拒操作不改变任何状态，且之后仍可正常使用。
	seq0, snapSeq0 := t.log.Seq(), t.snapSeq
	if !errors.Is(t.Put("", "x"), ErrEmptyKey) ||
		!errors.Is(t.Put("k", ""), ErrEmptyVal) ||
		!errors.Is(t.Del(""), ErrEmptyKey) {
		return fmt.Errorf("%w: inv4 reject", ErrSelfCheck)
	}
	if _, _, err := t.Read("a", t.snapSeq-1); !errors.Is(err, ErrBeforeSnap) {
		return fmt.Errorf("%w: inv4 read", ErrSelfCheck)
	}
	if t.log.Seq() != seq0 || t.snapSeq != snapSeq0 {
		return fmt.Errorf("%w: inv4 state", ErrSelfCheck)
	}
	return t.Put("ok", "1")
}
