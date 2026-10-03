// Package journal 提供按实例分区的追加式记录日志。
// 日志不理解记录语义，只保证同一实例内追加有序、读取一致。
package journal

import "sync"

// Kind 是日志记录的六种类型之一。
type Kind int

const (
	KindBegin      Kind = iota // B：创建实例
	KindIntent                 // I(i)：步骤 i 开始前的意图
	KindDone                   // D(i)：步骤 i 成功
	KindFail                   // F(i,k)：步骤 i 失败
	KindCompIntent             // CI(j)：补偿 j 开始
	KindCompDone               // CD(j)：补偿 j 完成
)

// FailKind 区分瞬时失败与永久失败。
type FailKind int

const (
	Transient FailKind = iota // 瞬时失败，可重试
	Permanent                 // 永久失败
)

// Record 是一条日志记录。Step 仅对 I/D/F/CI/CD 有意义。
type Record struct {
	Kind Kind
	Step int
	Fail FailKind
}

// Journal 是按实例分区的追加式日志，并发安全。
type Journal struct {
	mu   sync.RWMutex
	logs map[string][]Record
}

// New 返回空日志。
func New() *Journal {
	return &Journal{logs: make(map[string][]Record)}
}

// Append 向实例 id 的分区追加一条记录。
func (j *Journal) Append(id string, r Record) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.logs[id] = append(j.logs[id], r)
}

// Records 返回实例 id 的全部记录副本；不存在时返回 nil。
func (j *Journal) Records(id string) []Record {
	j.mu.RLock()
	defer j.mu.RUnlock()
	src := j.logs[id]
	out := make([]Record, len(src))
	copy(out, src)
	return out
}

// Len 返回实例 id 的记录条数。
func (j *Journal) Len(id string) int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return len(j.logs[id])
}
