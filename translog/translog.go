// Package translog 是分代事务日志：追加、落盘水位、断电截断、换代与重放。
package translog

// Op 表示一条写操作的类型。
type Op uint8

const (
	IndexOp Op = iota + 1
	DeleteOp
)

// Entry 是日志中的一条记录。
type Entry struct {
	Seq  int64
	Op   Op
	ID   string
	Body []byte
}

// TransLog 是分代事务日志。零值不可用，必须用 New 创建。
type TransLog struct {
	entries    []Entry // 当前代的全部已追加记录，seq 在代内连续递增
	synced     int64   // 落盘水位（跨代保持）
	generation int64
}

// New 创建第一代日志，synced 为初始落盘水位（通常为 0）。
func New(synced int64) *TransLog {
	return &TransLog{synced: synced, generation: 1}
}

// Append 追加一条记录并返回其在当前代内的偏移（0 起）。
func (l *TransLog) Append(seq int64, op Op, id string, body []byte) int64 {
	bodyCopy := append([]byte(nil), body...)
	l.entries = append(l.entries, Entry{Seq: seq, Op: op, ID: id, Body: bodyCopy})
	return int64(len(l.entries)) - 1
}

// Sync 把落盘水位推进到 seq（只能前移）。
func (l *TransLog) Sync(seq int64) {
	if seq > l.synced {
		l.synced = seq
	}
}

// Synced 返回当前落盘水位。
func (l *TransLog) Synced() int64 { return l.synced }

// Generation 返回当前代代号。
func (l *TransLog) Generation() int64 { return l.generation }

// Len 返回当前代的记录条数。
func (l *TransLog) Len() int64 { return int64(len(l.entries)) }

// Crash 模拟断电：丢弃 seq 大于 synced 的尾部记录。
func (l *TransLog) Crash() {
	kept := 0
	for _, entry := range l.entries {
		if entry.Seq <= l.synced {
			kept++
		} else {
			break
		}
	}
	l.entries = l.entries[:kept]
}

// Rotate 换到新一代：代号加 1 并丢弃旧代全部记录，水位保持不变。
func (l *TransLog) Rotate() {
	l.generation++
	l.entries = nil
}

// Replay 按序返回当前代中 seq 大于 committed 且不大于 synced 的记录副本。
func (l *TransLog) Replay(committed int64) []Entry {
	var out []Entry
	for _, entry := range l.entries {
		if entry.Seq > committed && entry.Seq <= l.synced {
			entry.Body = append([]byte(nil), entry.Body...)
			out = append(out, entry)
		}
	}
	return out
}
