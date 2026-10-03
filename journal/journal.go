// Package journal 实现按实例分区的追加式记录日志。
package journal

import (
	"sync"
	"sync/atomic"
)

// Kind 为记录类型，日志只有六种记录。
type Kind byte

const (
	KindBegin      Kind = iota // B：创建
	KindIntent                 // I(i)：步骤 i 开始前的意图
	KindDone                   // D(i)：步骤 i 成功
	KindFail                   // F(i,k)：步骤 i 失败
	KindCompIntent             // CI(j)：补偿 j 开始
	KindCompDone               // CD(j)：补偿 j 完成
)

// FailKind 为失败类别。
type FailKind byte

const (
	Transient FailKind = iota // 瞬时失败
	Permanent                 // 永久失败
)

// Record 为一条日志记录；Fail 仅在 Kind == KindFail 时有效。
type Record struct {
	Kind Kind
	Step int
	Fail FailKind
}

// shard 为单实例分区，独立加锁，实例间互不影响。
type shard struct {
	mu   sync.Mutex
	recs []Record
}

// Journal 为按实例分区的追加式日志。
type Journal struct {
	mu     sync.Mutex // 仅保护 shards 表本身
	shards map[string]*shard
	reads  atomic.Int64 // 非导出计数器：Read 累计读取的记录条数
}

// New 返回空日志。
func New() *Journal { return &Journal{shards: make(map[string]*shard)} }

func (j *Journal) shardOf(id string) *shard {
	j.mu.Lock()
	defer j.mu.Unlock()
	s, ok := j.shards[id]
	if !ok {
		s = &shard{}
		j.shards[id] = s
	}
	return s
}

// Append 向 id 的分区追加一条记录。
func (j *Journal) Append(id string, r Record) {
	s := j.shardOf(id)
	s.mu.Lock()
	s.recs = append(s.recs, r)
	s.mu.Unlock()
}

// Read 返回 id 分区日志的副本；读取条数恰为该分区长度，与他实例无关。
func (j *Journal) Read(id string) []Record {
	s := j.shardOf(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.recs))
	copy(out, s.recs)
	j.reads.Add(int64(len(s.recs)))
	return out
}

// Len 返回 id 分区的记录条数。
func (j *Journal) Len(id string) int {
	s := j.shardOf(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recs)
}
