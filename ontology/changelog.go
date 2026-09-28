package ontology

import (
	"sync"
	"time"
)

// LogEntry 是一条判定日志：完整记录输入批次、判定依据与输出的净变化，
// 使下游按顺序重放日志即可复现各组的去重计数。
type LogEntry struct {
	Seq       int64
	Time      time.Time
	Input     []Change
	Accepted  bool
	Reason    RejectReason
	EntryIdx  int
	Changes   []GroupDelta
	ViewAfter map[string]int
}

// Journal 是按序号追加的判定日志。所有方法均可被并发调用；
// Entries 返回逐字段一致的深拷贝快照，调用方对快照的修改不会影响日志。
type Journal struct {
	mu      sync.Mutex
	entries []LogEntry
}

// NewJournal 创建空日志。
func NewJournal() *Journal {
	return &Journal{}
}

// Append 原子追加一条日志并返回其分配到的序号（从 0 起）。
// 序号在日志内部按追加顺序生成；Time 字段保留调用方提供的值（含零值），
// 不注入墙钟时间，从而同一输入序列可复现出完全相同的日志。
// 入条目的切片与映射会被深拷贝后存储，与调用方隔离。
func (j *Journal) Append(entry LogEntry) int64 {
	j.mu.Lock()
	defer j.mu.Unlock()

	entry.Seq = int64(len(j.entries))
	entry.Input = cloneChanges(entry.Input)
	entry.Changes = cloneDeltas(entry.Changes)
	entry.ViewAfter = cloneView(entry.ViewAfter)
	j.entries = append(j.entries, entry)
	return entry.Seq
}

// Entries 返回日志当前内容的深拷贝快照，按序号升序排列。
func (j *Journal) Entries() []LogEntry {
	j.mu.Lock()
	defer j.mu.Unlock()

	out := make([]LogEntry, len(j.entries))
	for i, e := range j.entries {
		out[i] = LogEntry{
			Seq:       e.Seq,
			Time:      e.Time,
			Input:     cloneChanges(e.Input),
			Changes:   cloneDeltas(e.Changes),
			ViewAfter: cloneView(e.ViewAfter),
			Accepted:  e.Accepted,
			Reason:    e.Reason,
			EntryIdx:  e.EntryIdx,
		}
	}
	return out
}

// Len 返回日志条数。
func (j *Journal) Len() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.entries)
}

func cloneChanges(in []Change) []Change {
	if in == nil {
		return nil
	}
	out := make([]Change, len(in))
	copy(out, in)
	return out
}

func cloneDeltas(in []GroupDelta) []GroupDelta {
	if in == nil {
		return nil
	}
	out := make([]GroupDelta, len(in))
	copy(out, in)
	return out
}

func cloneView(in map[string]int) map[string]int {
	if in == nil {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
