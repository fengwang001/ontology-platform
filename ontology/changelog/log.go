// Package changelog 实现一条只追加（append-only）的按键写入日志，
// 以及对任意位点区间按键合并、只保留每个键最后一条写入的压缩器。
package changelog

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Record 是日志中的一条写入记录。
//
// 序号从 1 开始连续递增；Compacted 为 true 表示该记录是一次区间压缩
// 合并产生的压缩记录。
type Record struct {
	Seq       int64
	Key       string
	Value     string
	Compacted bool
}

// CompactResult 描述一次压缩的结果摘要。
type CompactResult struct {
	Left           int64
	Right          int64
	MergedRecords  int
	RemovedRecords int
}

// Log 是一条可并发访问的只追加日志。
type Log struct {
	writeMu sync.Mutex
	state   atomic.Pointer[state]
}

// state 是不可变快照：任何追加/压缩都复制出新快照后整体替换。
type state struct {
	nextSeq    int64
	records    []Record
	generation int64
}

// New 创建一条空日志，下一条写入的序号为 1。
func New() *Log {
	l := &Log{}
	l.state.Store(&state{nextSeq: 1, records: nil, generation: 0})
	return l
}

// NextSeq 返回下一条追加写入将使用的序号。
func (l *Log) NextSeq() int64 {
	return l.state.Load().nextSeq
}

// Append 在日志尾部追加一条写入，返回新记录的序号。
func (l *Log) Append(key string, value string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()

	old := l.state.Load()
	seq := old.nextSeq
	records := make([]Record, 0, len(old.records)+1)
	records = append(records, old.records...)
	records = append(records, Record{Seq: seq, Key: key, Value: value})

	l.state.Store(&state{
		nextSeq:    seq + 1,
		records:    records,
		generation: old.generation + 1,
	})
	return seq, nil
}

// ReadAt 返回位点 pos 上键 key 可见条目中序号最大者。
//
// 位点即序号上界：可见条目为所有满足 seq <= pos 的记录；
// 无可见条目时 ok 为 false、err 为 nil（不存在而非报错）。
func (l *Log) ReadAt(key string, pos int64) (Record, bool, error) {
	if key == "" {
		return Record{}, false, ErrEmptyKey
	}
	snap := l.state.Load()
	if pos < 1 || pos >= snap.nextSeq {
		return Record{}, false, ErrPositionOutOfRange
	}

	found := false
	var best Record
	for _, rec := range snap.records {
		if rec.Seq > pos {
			break
		}
		if rec.Key == key {
			best = rec
			found = true
		}
	}
	return best, found, nil
}

// Compact 对闭区间 [left, right] 内同一键的多条写入只保留最后一条。
//
// 规则：
//   - 区间外记录原样保留；区间内同一键只保留序号最大（最后一条）的写入，
//     合并为一条压缩记录（Compacted=true），序号沿用被保留记录的原序号，
//     区间内没有任何写入的键不产生压缩记录。
//   - 合并结果只取决于区间内条目，因此同一状态下重复压缩结果一致、可复现。
//   - 参数校验先于任何状态变更，失败不会改变日志与位点映射。
func (l *Log) Compact(left, right int64) (CompactResult, error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()

	old := l.state.Load()
	lastSeq := old.nextSeq - 1
	switch {
	case left < 1:
		return CompactResult{}, ErrCompactLeftTooSmall
	case right > lastSeq:
		return CompactResult{}, ErrCompactRightTooLarge
	case left > right:
		return CompactResult{}, ErrCompactInverted
	}

	// lastByKey：区间内每个键最后一条（序号最大）写入。
	// inRange 记录该键在区间内被合并掉的条目总数（含保留的那一条）。
	type merged struct {
		rec   Record
		count int
	}
	lastByKey := make(map[string]merged)
	for _, rec := range old.records {
		if rec.Seq < left {
			continue
		}
		if rec.Seq > right {
			break
		}
		cur := lastByKey[rec.Key]
		if cur.count == 0 || rec.Seq > cur.rec.Seq {
			cur.rec = rec
		}
		cur.count++
		lastByKey[rec.Key] = cur
	}

	keys := make([]string, 0, len(lastByKey))
	for key := range lastByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	kept := make(map[int64]Record, len(keys))
	removed := 0
	mergedCount := 0
	for _, key := range keys {
		entry := lastByKey[key]
		rec := entry.rec
		rec.Compacted = true
		kept[rec.Seq] = rec
		mergedCount++
		removed += entry.count - 1
	}

	records := make([]Record, 0, len(old.records)-removed)
	for _, rec := range old.records {
		if rec.Seq >= left && rec.Seq <= right {
			if replacement, ok := kept[rec.Seq]; ok {
				records = append(records, replacement)
			}
			continue
		}
		records = append(records, rec)
	}

	l.state.Store(&state{
		nextSeq:    old.nextSeq,
		records:    records,
		generation: old.generation + 1,
	})
	return CompactResult{
		Left:           left,
		Right:          right,
		MergedRecords:  mergedCount,
		RemovedRecords: removed,
	}, nil
}

// Verify 对当前日志做结构自检，返回快照序号。
//
// 自检只读不可变快照，可与读、追加、压缩并发调用。
func (l *Log) Verify() (int64, error) {
	snap := l.state.Load()
	if snap.nextSeq < 1 {
		return snap.generation, ErrLogCorrupted
	}
	var prev int64
	for _, rec := range snap.records {
		if rec.Seq < 1 || rec.Seq >= snap.nextSeq || rec.Seq <= prev {
			return snap.generation, ErrLogCorrupted
		}
		prev = rec.Seq
	}
	return snap.generation, nil
}

// Records 返回当前全部记录的一份拷贝（按序号升序），主要用于批量参照核对。
func (l *Log) Records() []Record {
	snap := l.state.Load()
	out := make([]Record, len(snap.records))
	copy(out, snap.records)
	return out
}

// Generation 返回当前状态代数：每次成功的追加或压缩使其单调加一。
func (l *Log) Generation() int64 {
	return l.state.Load().generation
}

// readSnapshot 在指定不可变快照上按位点语义读取，与 ReadAt 的判定完全一致。
func readSnapshot(snap *state, key string, pos int64) (Record, bool) {
	found := false
	var best Record
	for _, rec := range snap.records {
		if rec.Seq > pos {
			break
		}
		if rec.Key == key {
			best = rec
			found = true
		}
	}
	return best, found
}
