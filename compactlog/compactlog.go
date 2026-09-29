// Package compactlog 实现带删除墓碑保留的按键压实日志。
package compactlog

import (
	"fmt"
	"sort"
	"sync"
)

// Reason 区分操作被拒绝的原因。
type Reason int

const (
	// ReasonEmptyKey 表示写入了空键。
	ReasonEmptyKey Reason = iota
	// ReasonClockRegression 表示写入时间早于上次写入时间。
	ReasonClockRegression
	// ReasonLogFull 表示日志已达到容量上限。
	ReasonLogFull
	// ReasonConsumerNotSubscribed 表示读取者未订阅。
	ReasonConsumerNotSubscribed
)

func (r Reason) String() string {
	switch r {
	case ReasonEmptyKey:
		return "empty key"
	case ReasonClockRegression:
		return "clock regression"
	case ReasonLogFull:
		return "log full"
	case ReasonConsumerNotSubscribed:
		return "consumer not subscribed"
	default:
		return "unknown reason"
	}
}

// Error 是被拒绝操作返回的错误，携带可区分的原因。
type Error struct {
	Reason Reason
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("compactlog: %s: %s", e.Reason, e.Detail)
}

// Record 是日志中的一条记录。Value 为 nil 表示删除墓碑。
type Record struct {
	Seq   uint64
	Key   string
	Value []byte
	Time  int64
}

// Tombstone 报告该记录是否为删除墓碑。
func (r Record) Tombstone() bool { return r.Value == nil }

// consumer 保存一个订阅者的读取位置与物化视图。
type consumer struct {
	pos  uint64 // 已读到的最大序号；下一条读取从 pos 之后开始
	view map[string][]byte
}

// Log 是按键压实的追加日志。所有方法均可并发调用。
type Log struct {
	mu        sync.Mutex
	records   map[uint64]Record // 稀疏存储：压实只留空洞，序号不重排
	seqs      []uint64          // 升序的存活序号
	nextSeq   uint64            // 下一条记录的序号，从 1 开始
	clock     int64             // 最后一次成功写入的时间
	capacity  int               // 日志最多容纳的记录条数
	retention int64             // 墓碑保留时长（与写入时间同一时钟域）
	consumers map[string]*consumer
}

// NewLog 创建容量为 capacity、墓碑保留时长为 retention 的日志。
func NewLog(capacity int, retention int64) *Log {
	return &Log{
		records:   make(map[uint64]Record),
		nextSeq:   1,
		capacity:  capacity,
		retention: retention,
		consumers: make(map[string]*consumer),
	}
}

// Write 追加一条记录。value 为 nil 表示删除该键（写入墓碑）。
// 空键、时间回退、日志已满会被拒绝，且被拒绝的写入不改变
// 时钟、序号计数器与日志内容。
func (l *Log) Write(key string, value []byte, now int64) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if key == "" {
		return 0, &Error{Reason: ReasonEmptyKey, Detail: "key must not be empty"}
	}
	if now < l.clock {
		return 0, &Error{
			Reason: ReasonClockRegression,
			Detail: fmt.Sprintf("write time %d is before last write time %d", now, l.clock),
		}
	}
	if len(l.records) >= l.capacity {
		return 0, &Error{
			Reason: ReasonLogFull,
			Detail: fmt.Sprintf("log holds %d records, capacity %d", len(l.records), l.capacity),
		}
	}

	seq := l.nextSeq
	var stored []byte
	if value != nil {
		stored = make([]byte, len(value))
		copy(stored, value)
	}
	l.records[seq] = Record{Seq: seq, Key: key, Value: stored, Time: now}
	l.seqs = append(l.seqs, seq)
	l.nextSeq++
	l.clock = now
	return seq, nil
}

// Compact 压实日志：每个键只保留序号最大的记录；最新记录为墓碑
// 且写入时间距今超过保留期（now - Time > retention）时一并清除。
// 恰好达到保留期的墓碑仍然保留。压实只留空洞，不重排序号。
func (l *Log) Compact(now int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	latest := make(map[string]uint64, len(l.records))
	for _, seq := range l.seqs { // 按序号升序遍历，后者覆盖前者
		latest[l.records[seq].Key] = seq
	}

	keep := make(map[uint64]struct{}, len(latest))
	for _, seq := range latest {
		rec := l.records[seq]
		if rec.Tombstone() && now-rec.Time > l.retention {
			continue // 墓碑已超过保留期，清除
		}
		keep[seq] = struct{}{}
	}

	kept := l.seqs[:0]
	for _, seq := range l.seqs {
		if _, ok := keep[seq]; ok {
			kept = append(kept, seq)
		} else {
			delete(l.records, seq)
		}
	}
	l.seqs = kept
}

// Subscribe 注册一个从头开始读取的消费者。
func (l *Log) Subscribe(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.consumers[name]; !ok {
		l.consumers[name] = &consumer{view: make(map[string][]byte)}
	}
}

// ReadNext 让消费者读取其位置之后的下一条存活记录（跳过空洞），
// 并把该记录应用到消费者自己的视图上。返回 ok=false 表示已追平。
// 未订阅的消费者会被拒绝，且拒绝不改变任何位置与视图。
func (l *Log) ReadNext(name string) (rec Record, ok bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	c, ok := l.consumers[name]
	if !ok {
		return Record{}, false, &Error{
			Reason: ReasonConsumerNotSubscribed,
			Detail: fmt.Sprintf("consumer %q has not subscribed", name),
		}
	}

	idx := sort.Search(len(l.seqs), func(i int) bool { return l.seqs[i] > c.pos })
	if idx == len(l.seqs) {
		return Record{}, false, nil // 已追平
	}

	rec = l.records[l.seqs[idx]]
	c.pos = rec.Seq
	if rec.Tombstone() {
		delete(c.view, rec.Key)
	} else {
		v := make([]byte, len(rec.Value))
		copy(v, rec.Value)
		c.view[rec.Key] = v
	}
	return rec, true, nil
}

// View 返回消费者当前物化视图的副本。
func (l *Log) View(name string) (map[string][]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.consumers[name]
	if !ok {
		return nil, &Error{
			Reason: ReasonConsumerNotSubscribed,
			Detail: fmt.Sprintf("consumer %q has not subscribed", name),
		}
	}
	view := make(map[string][]byte, len(c.view))
	for k, v := range c.view {
		cp := make([]byte, len(v))
		copy(cp, v)
		view[k] = cp
	}
	return view, nil
}

// Snapshot 按序号升序返回当前存活记录的副本（含空洞）。
func (l *Log) Snapshot() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Record, 0, len(l.seqs))
	for _, seq := range l.seqs {
		out = append(out, l.records[seq])
	}
	return out
}

// NextSeq 返回下一条写入将获得的序号。
func (l *Log) NextSeq() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.nextSeq
}

// Clock 返回最后一次成功写入的时间。
func (l *Log) Clock() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.clock
}
