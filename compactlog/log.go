package compactlog

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Record 是日志中的一条记录。序号按追加顺序分配且永不重编号。
type Record struct {
	Seq       uint64
	Key       string
	Value     string
	Tombstone bool
	Timestamp time.Time
}

// Consumer 记录一个订阅者的读取位置与物化视图。
type Consumer struct {
	ID   uint64
	Next uint64 // 下一条待读序号（跳过空洞）
	View map[string]string
}

// Log 是支持压实与墓碑保留的按键压实日志。
type Log struct {
	mu        sync.Mutex
	clock     Clock
	retention time.Duration
	capacity  int

	nextSeq   uint64
	entries   map[uint64]Record // 压实后留空洞
	lastWrite time.Time
	hasWrite  bool

	nextConsumerID uint64
	consumers      map[uint64]*Consumer
}

// New 创建日志。retention 为墓碑保留期，capacity 为日志可容纳的最大记录数。
func New(clock Clock, retention time.Duration, capacity int) *Log {
	return &Log{
		clock:     clock,
		retention: retention,
		capacity:  capacity,
		nextSeq:   1,
		entries:   make(map[uint64]Record),
		consumers: make(map[uint64]*Consumer),
	}
}

// Append 追加一条写入记录。
func (l *Log) Append(key, value string) (Record, error) {
	return l.append(key, value, false)
}

// Delete 追加一条墓碑记录。
func (l *Log) Delete(key string) (Record, error) {
	return l.append(key, "", true)
}

// append 校验并追加记录；任何校验失败都不改变时钟、序号、日志与消费者状态。
func (l *Log) append(key, value string, tombstone bool) (Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if key == "" {
		return Record{}, ErrEmptyKey
	}
	now := l.clock.Now()
	if l.hasWrite && now.Before(l.lastWrite) {
		return Record{}, ErrClockRegression
	}
	if len(l.entries) >= l.capacity {
		return Record{}, ErrLogFull
	}

	rec := Record{
		Seq:       l.nextSeq,
		Key:       key,
		Value:     value,
		Tombstone: tombstone,
		Timestamp: now,
	}
	l.entries[rec.Seq] = rec
	l.nextSeq++
	l.lastWrite = now
	l.hasWrite = true
	return rec, nil
}

// Compact 压实日志：每个键只保留最新记录，并清除写入时间距今超过保留期的墓碑。
// 序号不重编号，被清除的记录留下空洞；墓碑年龄恰好等于保留期时保留。
func (l *Log) Compact() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	if l.hasWrite && now.Before(l.lastWrite) {
		return ErrClockRegression
	}

	latest := make(map[string]uint64, len(l.entries))
	for seq, rec := range l.entries {
		if cur, ok := latest[rec.Key]; !ok || seq > cur {
			latest[rec.Key] = seq
		}
	}

	keep := make(map[uint64]struct{}, len(latest))
	for _, seq := range latest {
		keep[seq] = struct{}{}
	}
	for seq, rec := range l.entries {
		if _, ok := keep[seq]; !ok {
			delete(l.entries, seq)
			continue
		}
		if rec.Tombstone && now.Sub(rec.Timestamp) > l.retention {
			delete(l.entries, seq)
		}
	}
	return nil
}

// Subscribe 注册新消费者，从日志起点开始读取，返回消费者 ID。
func (l *Log) Subscribe() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextConsumerID++
	id := l.nextConsumerID
	l.consumers[id] = &Consumer{ID: id, Next: 1, View: make(map[string]string)}
	return id
}

// Read 让消费者按序号顺序读取至多 max 条记录并应用到视图，返回本次应用的记录。
// 空洞被跳过；墓碑从视图中移除对应键。
func (l *Log) Read(consumerID uint64, max int) ([]Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	c, ok := l.consumers[consumerID]
	if !ok {
		return nil, ErrUnknownConsumer
	}
	if max <= 0 {
		return nil, nil
	}

	var applied []Record
	for seq := c.Next; seq < l.nextSeq && len(applied) < max; seq++ {
		rec, ok := l.entries[seq]
		if !ok {
			continue // 压实留下的空洞
		}
		if rec.Tombstone {
			delete(c.View, rec.Key)
		} else {
			c.View[rec.Key] = rec.Value
		}
		applied = append(applied, rec)
		c.Next = seq + 1
	}
	return applied, nil
}

// Snapshot 返回日志当前保留的记录（按序号升序），用于观察与测试。
func (l *Log) Snapshot() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sortedEntries()
}

// View 返回消费者视图的确定性拷贝。
func (l *Log) View(consumerID uint64) (map[string]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.consumers[consumerID]
	if !ok {
		return nil, ErrUnknownConsumer
	}
	view := make(map[string]string, len(c.View))
	for k, v := range c.View {
		view[k] = v
	}
	return view, nil
}

func (l *Log) sortedEntries() []Record {
	seqs := make([]int, 0, len(l.entries))
	for seq := range l.entries {
		seqs = append(seqs, int(seq))
	}
	sort.Ints(seqs)
	out := make([]Record, 0, len(seqs))
	for _, seq := range seqs {
		out = append(out, l.entries[uint64(seq)])
	}
	return out
}

func (r Record) String() string {
	kind := "PUT"
	if r.Tombstone {
		kind = "DEL"
	}
	return fmt.Sprintf("#%d %s %q=%q @%s", r.Seq, kind, r.Key, r.Value, r.Timestamp.Format(time.RFC3339Nano))
}
