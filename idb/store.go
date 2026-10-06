package idb

import "bytes"

// ObjectStore 是按键存放值的多版本对象仓库。
// 每个键保留一条版本链；写入只追加，绝不就地改动历史版本，
// 因而读快照与提交可见性只与版本号比较有关，开销不随无关键数增长。
type ObjectStore struct {
	name    string
	entries map[string][]versionedValue
}

type versionedValue struct {
	seq   uint64
	value []byte
	alive bool
}

func newObjectStore(name string) *ObjectStore {
	return &ObjectStore{name: name, entries: make(map[string][]versionedValue)}
}

// readAt 返回版本号不大于 seq 的最新可见值；不存在时 ok=false（非错误）。
func (s *ObjectStore) readAt(key string, seq uint64) (value []byte, ok bool) {
	chain := s.entries[key]
	for i := len(chain) - 1; i >= 0; i-- {
		v := chain[i]
		if v.seq <= seq {
			if !v.alive {
				return nil, false
			}
			return v.value, true
		}
	}
	return nil, false
}

// append 追加一个新版本（alive=false 表示删除标记）。
func (s *ObjectStore) append(key string, seq uint64, value []byte, alive bool) {
	s.entries[key] = append(s.entries[key], versionedValue{
		seq:   seq,
		value: bytes.Clone(value),
		alive: alive,
	})
}

// clone 深拷贝整个仓库（版本变更中止时恢复整个仓库集合用）。
func (s *ObjectStore) clone() *ObjectStore {
	cp := &ObjectStore{name: s.name, entries: make(map[string][]versionedValue, len(s.entries))}
	for k, chain := range s.entries {
		cc := make([]versionedValue, len(chain))
		for i, v := range chain {
			cc[i] = versionedValue{seq: v.seq, value: bytes.Clone(v.value), alive: v.alive}
		}
		cp.entries[k] = cc
	}
	return cp
}
