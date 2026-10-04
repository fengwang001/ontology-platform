// Package translog 实现事务日志：追加、落盘水位、按水位截断与换代。
//
// 日志只保存条目序列与落盘水位 synced，不感知文档语义。
// 条目按 seq 单调递增追加；Crash 丢弃水位之后的尾部，
// Rollover 在提交点建立后换到新一代并丢弃旧代全部条目。
// 并发安全由调用方（engine）保证。
package translog

// Kind 是日志条目的操作类型。
type Kind uint8

const (
	KindIndex Kind = iota + 1
	KindDelete
)

// Op 是一条日志条目，对应一个被接受的写操作。
type Op struct {
	Seq  int64
	Kind Kind
	ID   []byte
	Body []byte
}

// Log 是一代事务日志。
type Log struct {
	generation int64
	entries    []Op
	synced     int64
}

// New 创建指定代号的空日志。
func New(generation int64) *Log {
	return &Log{generation: generation}
}

// Append 追加一条已接受的操作。
func (l *Log) Append(op Op) {
	l.entries = append(l.entries, op)
}

// Len 返回当前代中未提交的条目数。
func (l *Log) Len() int {
	return len(l.entries)
}

// Generation 返回当前代号。
func (l *Log) Generation() int64 {
	return l.generation
}

// Synced 返回落盘水位：已落盘的最大序号。
func (l *Log) Synced() int64 {
	return l.synced
}

// SyncTo 把落盘水位推进到 seq（只升不降）。
func (l *Log) SyncTo(seq int64) {
	if seq > l.synced {
		l.synced = seq
	}
}

// Entries 返回当前代全部条目的拷贝，按 seq 升序。
func (l *Log) Entries() []Op {
	return append([]Op(nil), l.entries...)
}

// Crash 模拟断电：丢弃 seq 大于落盘水位的尾部条目。
func (l *Log) Crash() {
	keep := 0
	for keep < len(l.entries) && l.entries[keep].Seq <= l.synced {
		keep++
	}
	l.entries = l.entries[:keep]
}

// Rollover 换到新一代：代号加 1，旧代条目全部丢弃。
func (l *Log) Rollover() {
	l.generation++
	l.entries = nil
}
