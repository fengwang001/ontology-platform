// Package lsm 实现 SSTable 列表（落盘序）与合并，依赖 mem。
package lsm

import (
	"sync"

	"ontology/mem"
)

// sstable 不可变，按 key 存条目，map 定位（非线性扫描）。
type sstable struct {
	m map[string]mem.Entry
}

// Store = memtable + SSTable 列表（旧→新）。并发安全。
type Store struct {
	mu      sync.Mutex
	maxMem  int
	mt      *mem.Table
	ssts    []sstable
	seq     uint64
	readAmp int // 最近一次 Get 实际检查过的 SSTable 个数
	probe   int // 最近一次 Get 在单个 SSTable 内定位 key 检查的条目数（非导出，不进公开接口）
}

// NewStore 建一个 memtable 容量为 maxMem 的存储。
func NewStore(maxMem int) *Store {
	return &Store{maxMem: maxMem, mt: mem.New(maxMem)}
}

// write：memtable 满则先冻结再写入本条；每条写占一个全局递增 seq。
func (s *Store) write(key string, e mem.Entry) {
	if s.mt.Full() {
		s.freeze()
	}
	s.seq++
	e.Seq = s.seq
	s.mt.Put(key, e)
}

func (s *Store) Put(key string, val int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.write(key, mem.Entry{Val: val})
}

func (s *Store) Del(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.write(key, mem.Entry{Tomb: true})
}

// freeze 把当前 memtable 冻结为列表末尾（最新）的 SSTable，并清空 memtable。
func (s *Store) freeze() {
	t := sstable{m: make(map[string]mem.Entry, s.mt.Len())}
	for _, kv := range s.mt.Items() {
		t.m[kv.Key] = kv.E
	}
	s.ssts = append(s.ssts, t)
	s.mt = mem.New(s.maxMem)
}

// Get 先查 memtable，再从最新 SSTable 向最旧查，首个命中决定结果。
// 返回 (val, ok, deleted)：值命中 ok=true；墓碑 deleted=true；全无则皆 false。
func (s *Store) Get(key string) (val int64, ok bool, deleted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readAmp, s.probe = 0, 0
	if e, hit := s.mt.Get(key); hit {
		return e.Val, !e.Tomb, e.Tomb
	}
	for i := len(s.ssts) - 1; i >= 0; i-- {
		s.readAmp++
		s.probe = 1 // map 定位：单次只检查 1 个条目，与表内条目数无关
		if e, hit := s.ssts[i].m[key]; hit {
			return e.Val, !e.Tomb, e.Tomb
		}
	}
	return 0, false, false
}

// Compact 把所有 SSTable 合并成一个：逐 key 取 seq 最大者为胜者；
// 胜者是值则保留；胜者是墓碑且该 key 在合并范围内还有更旧的值则保留墓碑，
// 否则丢弃墓碑、key 直接删除。
func (s *Store) Compact() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ssts) == 0 {
		return
	}
	type agg struct {
		win      mem.Entry
		olderVal bool // 合并范围内是否存在 seq 更小的值条目
	}
	best := make(map[string]*agg)
	for _, t := range s.ssts {
		for k, e := range t.m {
			a := best[k]
			if a == nil {
				a = &agg{}
				best[k] = a
			}
			if e.Seq > a.win.Seq {
				if a.win.Seq != 0 && !a.win.Tomb {
					a.olderVal = true // 旧胜者沦为更旧的值
				}
				a.win = e
			} else if !e.Tomb {
				a.olderVal = true
			}
		}
	}
	merged := sstable{m: make(map[string]mem.Entry, len(best))}
	for k, a := range best {
		if a.win.Tomb && !a.olderVal {
			continue // 墓碑是唯一条目：丢弃
		}
		merged.m[k] = a.win
	}
	s.ssts = []sstable{merged}
}

// ReadAmp 返回最近一次 Get 实际检查过的 SSTable 个数。
func (s *Store) ReadAmp() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAmp
}

// NumSST 返回当前 SSTable 个数。
func (s *Store) NumSST() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ssts)
}
