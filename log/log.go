// Package log 提供 Raft 日志条目与 1 基下标访问。log[0] 是哨兵（term=0）。
package log

// Entry 是一条日志条目，本题只关心任期。
type Entry struct {
	Term int
}

// Log 是 1 基下标的日志：entries[0] 为哨兵，条目 i 位于 entries[i]。
type Log struct {
	entries []Entry
}

// New 建一条日志，依次追加任期 terms 的条目（下标 1 起）。
func New(terms ...int) *Log {
	l := &Log{entries: make([]Entry, 1, len(terms)+1)}
	for _, t := range terms {
		l.entries = append(l.entries, Entry{Term: t})
	}
	return l
}

// Term 返回下标 i 处条目的任期；i 必须在 [0, Len()] 内。
func (l *Log) Term(i int) int { return l.entries[i].Term }

// Len 返回最后一条条目的下标（哨兵不计）。
func (l *Log) Len() int { return len(l.entries) - 1 }

// Append 在末尾追加一条任期 term 的条目。
func (l *Log) Append(term int) { l.entries = append(l.entries, Entry{Term: term}) }
