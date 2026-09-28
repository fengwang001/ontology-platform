// Package lwwset 实现最后写入者胜出（LWW）的元素集合 CRDT。
//
// 每个副本对每个元素保存最新添加时间与最新删除时间两条记录，
// 添加与删除分别取时间戳较大者；元素在集合中当且仅当存在添加时间
// 且删除时间不存在或严格小于添加时间（相等视为删除，即并列偏删除）。
package lwwset

import "sync"

// Record 是单个元素的两条时间戳记录。
type Record struct {
	Add    int64 // 最新添加时间戳，0 表示从未添加
	Remove int64 // 最新删除时间戳，0 表示从未删除
}

// Alive 判定元素是否在集合中：有添加时间且删除时间不存在或严格小于添加时间。
func (r Record) Alive() bool {
	return r.Add > 0 && r.Remove < r.Add
}

// changeEntry 是一条变更日志，记录某次变更后该元素记录的快照。
type changeEntry struct {
	seq    uint64
	elem   string
	record Record
}

// Set 是一个 LWW 元素集合副本。所有方法均可并发调用。
type Set struct {
	mu       sync.RWMutex
	id       string
	maxElems int
	records  map[string]Record
	seq      uint64            // 变更序号，单调递增
	log      []changeEntry     // 变更日志，供增量合并使用
	mergePos map[string]uint64 // 每个源副本已合并到的变更序号
}

// New 创建一个副本。id 为副本编号（非空），maxElements 为记录元素数上限（正整数）。
func New(id string, maxElements int) (*Set, error) {
	if id == "" {
		return nil, ErrInvalidReplicaID
	}
	if maxElements <= 0 {
		return nil, ErrInvalidLimit
	}
	return &Set{
		id:       id,
		maxElems: maxElements,
		records:  make(map[string]Record),
		mergePos: make(map[string]uint64),
	}, nil
}

// ID 返回副本编号。
func (s *Set) ID() string {
	return s.id
}
