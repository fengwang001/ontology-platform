// Package snap 实现日志追加、序号推进与快照生成，不依赖其他包。
package snap

// Op 是日志操作类型。
type Op int

const (
	Put Op = iota
	Del
)

// Entry 是一条日志记录，Seq 从 1 开始单调递增。
type Entry struct {
	Seq int64
	Key string
	Op  Op
	Val string
}

// Log 是只增不减的日志；快照不清空它。
type Log struct {
	entries []Entry
	seq     int64
}

// Append 推进 Seq 并追加一条记录，返回该记录。
func (l *Log) Append(key string, op Op, val string) Entry {
	l.seq++
	e := Entry{Seq: l.seq, Key: key, Op: op, Val: val}
	l.entries = append(l.entries, e)
	return e
}

// Seq 返回当前序号。
func (l *Log) Seq() int64 { return l.seq }

// Entries 返回全部日志的副本（调用方修改不影响日志）。
func (l *Log) Entries() []Entry {
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Build 对 Seq <= upTo 的日志逐键取 Seq 最大那条，Del 视为删除，
// 冻结成一份不可变快照（snapSeq = upTo）。
func Build(entries []Entry, upTo int64) (map[string]string, int64) {
	type last struct {
		op  Op
		val string
	}
	best := make(map[string]last)
	for _, e := range entries {
		if e.Seq > upTo {
			continue
		}
		best[e.Key] = last{op: e.Op, val: e.Val} // 日志按 Seq 升序，后者即最大
	}
	snap := make(map[string]string, len(best))
	for k, v := range best {
		if v.op == Put {
			snap[k] = v.val
		}
	}
	return snap, upTo
}
