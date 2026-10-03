package meta

import (
	"errors"
	"sync"
)

var (
	ErrInvalid  = errors.New("参数非法")
	ErrNotFound = errors.New("不存在")
)

const (
	LevelSize = 1
	LevelEtag = 2
	LevelTags = 3
)

type Record struct {
	Level int
	Size  int64
	Etag  *string           // nil 表示缺省；指向 "" 表示提供了空串
	Tags  map[string]string // nil 表示缺省；非 nil 空 map 表示提供了空集合
}

// Stats 为各级别记录条数（下标 0 对应级别 1）。
type Stats struct {
	CountByLvl [3]int
}

// Residual 描述级别大于 target 的残留记录。
type Residual struct {
	Count     int
	HighLevel int
}

// Store 是带格式级别的元数据记录存储。
// 调用方（gate）负责节点鉴权与字段级别判定；Store 只保证记录级别恒等
// 于写入时给定的 level，并维护按级别的计数，使残留判定为 O(1)。
type Store struct {
	mu      sync.RWMutex
	records map[string]Record
	counts  [3]int

	// scanned 仅被朴素扫描路径 ScanAbove 使用；Residue 只读计数器，
	// 因此正常回退流程中 scanned 恒为 0。
	scanned int
}

func New() *Store {
	return &Store{records: make(map[string]Record)}
}

// Put 以 level 写入（或覆盖）key 的记录。覆盖时旧级别计数减一、
// 新级别计数加一。
func (s *Store) Put(key string, rec Record) error {
	if key == "" || rec.Level < 1 || rec.Level > 3 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.records[key]; ok {
		s.counts[old.Level-1]--
	}
	s.records[key] = rec
	s.counts[rec.Level-1]++
	return nil
}

// Get 返回记录副本。
func (s *Store) Get(key string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[key]
	return rec, ok
}

func (s *Store) Exists(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.records[key]
	return ok
}

// Delete 删除记录并使对应级别计数减一。
func (s *Store) Delete(key string) error {
	if key == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[key]
	if !ok {
		return ErrNotFound
	}
	s.counts[rec.Level-1]--
	delete(s.records, key)
	return nil
}

// Stats 返回按级别的记录条数（副本）。
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{CountByLvl: s.counts}
}

// Residue 基于级别计数判定是否存在 level>target 的残留：
// 等于 target 的不算，大于 target 的任一非空即有残留。
// 全程只读三个计数器，不扫描任何记录。
func (s *Store) Residue(target int) (Residual, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := Residual{}
	for lvl := 3; lvl > target; lvl-- {
		if n := s.counts[lvl-1]; n > 0 {
			res.Count += n
			if res.HighLevel == 0 {
				res.HighLevel = lvl // 从高级别向下遍历，首次命中即最高级别
			}
		}
	}
	return res, res.Count > 0
}

// ScanAbove 是被放弃的朴素方案：逐条扫描级别大于 target 的记录。
// gate 不调用本方法；保留它以便测试证明正式路径 scanned==0。
func (s *Store) ScanAbove(target int) (Residual, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := Residual{}
	for _, rec := range s.records {
		s.scanned++
		if rec.Level > target {
			res.Count++
			if rec.Level > res.HighLevel {
				res.HighLevel = rec.Level
			}
		}
	}
	return res, res.Count > 0
}

// Scanned 返回朴素扫描累计扫描的记录数。正式路径下恒为 0。
func (s *Store) Scanned() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scanned
}
