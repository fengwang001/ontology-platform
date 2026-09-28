// Package ontology 提供带删除墓碑保留的按键压实日志。
package ontology

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// 可区分的拒绝原因。被拒绝的操作不会改变任何状态。
var (
	ErrEmptyKey         = errors.New("ontology: key must not be empty")
	ErrTimeRolledBack   = errors.New("ontology: record time must not be earlier than the clock")
	ErrLogFull          = errors.New("ontology: log capacity exhausted, compact before writing")
	ErrConsumerNotFound = errors.New("ontology: consumer is not subscribed")
	ErrDuplicateConsumer = errors.New("ontology: consumer name already subscribed")

	// ErrNoRecord 表示没有新的可读记录，消费者已读到日志末尾。
	ErrNoRecord = io.EOF
)

// entry 是日志中的一条不可变记录。
type entry struct {
	seq     int64
	key     string
	value   string
	deleted bool
	written time.Time
}

// CompactionLog 是按键压实的追加日志。
//
// 序号按追加顺序单调分配且永不重编号；压实只在原位置留下空洞。
// 每个键的最新记录始终保留；更早的同键记录以及超过墓碑保留期的删除
// 记录会在压实中被清除。
type CompactionLog struct {
	mu         sync.RWMutex
	capacity   int
	retention  time.Duration
	now        func() time.Time
	clock      time.Time
	nextSeq    int64
	records    []*entry // 按 seq 排序的槽位，已压实的位置为 nil
	liveCount  int
	consumers  map[string]*Consumer
}

// RecordView 是一条日志记录的只读视图，用于快照与日志输出。
type RecordView struct {
	Seq     int64
	Key     string
	Value   string
	Deleted bool
	Written time.Time
}

// Option 配置 CompactionLog。
type Option func(*CompactionLog)

// WithClock 注入时钟源，决定零时间参数写入/压实时使用的当前时间，
// 并便于测试做确定性验证。
func WithClock(now func() time.Time) Option {
	return func(l *CompactionLog) { l.now = now }
}

// NewCompactionLog 创建压实日志。
//
// capacity 是未压实（存活）记录的上限；写满后必须先 Compact 才能继续写。
// tombstoneRetention 是删除墓碑在压实中的最短存活时间：墓碑写入时间距今
// 超过该时长才会被清除，恰好达到保留期即视为过期。
func NewCompactionLog(capacity int, tombstoneRetention time.Duration, opts ...Option) *CompactionLog {
	l := &CompactionLog{
		capacity:  capacity,
		retention: tombstoneRetention,
		now:       time.Now,
		consumers: make(map[string]*Consumer),
	}
	for _, opt := range opts {
		opt(l)
	}
	l.clock = l.now()
	return l
}

// append 是 Put/Delete 的公共路径。deleted 为 true 时 value 被忽略，
// 写入一条墓碑。
func (l *CompactionLog) append(key, value string, at time.Time, deleted bool) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	when := at
	if when.IsZero() {
		when = l.now()
	}
	if when.Before(l.clock) {
		return 0, ErrTimeRolledBack
	}
	if l.capacity > 0 && l.liveCount >= l.capacity {
		return 0, ErrLogFull
	}

	seq := l.nextSeq + 1
	l.records = append(l.records, &entry{
		seq:     seq,
		key:     key,
		value:   value,
		deleted: deleted,
		written: when,
	})
	l.nextSeq = seq
	l.clock = when
	l.liveCount++
	return seq, nil
}

// Put 写入一个键值。
func (l *CompactionLog) Put(key, value string, at time.Time) (int64, error) {
	return l.append(key, value, at, false)
}

// Delete 为键写入一条删除墓碑。
func (l *CompactionLog) Delete(key string, at time.Time) (int64, error) {
	return l.append(key, "", at, true)
}

// Compact 压实日志：移除被覆盖的旧记录与过期墓碑。
//
// 规则：
//   - 每个键只保留序号最大（最新）的记录，其余同键记录删除；
//   - 最新记录若为墓碑且写入时间距今超过保留期（恰好达到即过期），删除；
//   - 槽位置空形成空洞，序号不重编号。
//
// 压实不推进时钟（即使传入未来时间），也不触碰消费者位置与视图。
func (l *CompactionLog) Compact(at time.Time) (removed int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := at
	if now.IsZero() {
		now = l.now()
	}

	latest := make(map[string]int) // key -> records 中最新记录的下标
	for i, rec := range l.records {
		if rec != nil {
			latest[rec.key] = i
		}
	}

	latestBySeq := make(map[int64]int, len(latest))
	for _, idx := range latest {
		latestBySeq[l.records[idx].seq] = idx
	}

	for i, rec := range l.records {
		if rec == nil {
			continue
		}
		keepIdx, isLatest := latestBySeq[rec.seq]
		if !isLatest || keepIdx != i {
			l.records[i] = nil
			removed++
			continue
		}
		if rec.deleted && now.Sub(rec.written) >= l.retention {
			l.records[i] = nil
			delete(latest, rec.key)
			removed++
		}
	}
	l.liveCount -= removed
	return removed, nil
}

// Subscribe 注册一个消费者，视图为空、位置位于日志开头。
// 消费者从当前日志的第一条记录开始按序号追赶；已被压实成空洞的序号
// 会在读取时自动跳过。重复注册同名消费者会被拒绝。
func (l *CompactionLog) Subscribe(name string) (*Consumer, error) {
	if name == "" {
		return nil, ErrEmptyKey
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.consumers[name]; ok {
		return nil, ErrDuplicateConsumer
	}
	c := &Consumer{
		name: name,
		log:  l,
		view: make(map[string]string),
	}
	l.consumers[name] = c
	return c, nil
}

// Unsubscribe 注销消费者。
func (l *CompactionLog) Unsubscribe(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.consumers[name]; !ok {
		return ErrConsumerNotFound
	}
	delete(l.consumers, name)
	return nil
}

// Snapshot 返回当前存活记录的只读快照（按序号升序，不含空洞）。
func (l *CompactionLog) Snapshot() []RecordView {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]RecordView, 0, l.liveCount)
	for _, rec := range l.records {
		if rec == nil {
			continue
		}
		out = append(out, RecordView{
			Seq:     rec.seq,
			Key:     rec.key,
			Value:   rec.value,
			Deleted: rec.deleted,
			Written: rec.written,
		})
	}
	return out
}

// LiveCount 返回当前存活（未压实）记录数。
func (l *CompactionLog) LiveCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.liveCount
}

// Dump 返回日志当前状态的确定性文本表示：存活记录按序号排列，
// 空洞以 hole 标记，便于测试日志与排查。
func (l *CompactionLog) Dump() string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var b strings.Builder
	fmt.Fprintf(&b, "log(nextSeq=%d, live=%d/%d):", l.nextSeq, l.liveCount, l.capacity)
	if len(l.records) == 0 {
		b.WriteString(" <empty>")
		return b.String()
	}
	for _, rec := range l.records {
		if rec == nil {
			b.WriteString("\n  [hole]")
			continue
		}
		if rec.deleted {
			fmt.Fprintf(&b, "\n  seq=%d %s=<tombstone @%s>", rec.seq, rec.key, rec.written.Format(time.RFC3339Nano))
		} else {
			fmt.Fprintf(&b, "\n  seq=%d %s=%q @%s", rec.seq, rec.key, rec.value, rec.written.Format(time.RFC3339Nano))
		}
	}
	return b.String()
}

// Consumer 按序号顺序读取日志，跳过空洞并维护自己的键视图。
type Consumer struct {
	name string
	log  *CompactionLog
	mu   sync.Mutex
	read int64 // 已处理到的序号
	view map[string]string
}

// Read 读取下一条可读记录，将其应用到消费者视图。
//
// 顺序扫描消费者位置之后的槽位，跳过压实留下的空洞，把遇到的第一条
// 存活记录应用到视图：普通写入覆盖键值，墓碑删除键。读到日志末尾且没有
// 可读记录时返回 ErrNoRecord（io.EOF），位置与视图不变。
// 对已注销（未订阅）的消费者读取返回 ErrConsumerNotFound。
func (c *Consumer) Read() (seq int64, key, value string, deleted bool, err error) {
	l := c.log
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.consumers[c.name] != c {
		return 0, "", "", false, ErrConsumerNotFound
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	idx := c.read // records[i] 的序号为 i+1
	for idx < int64(len(l.records)) {
		rec := l.records[idx]
		idx++
		if rec == nil {
			continue // 空洞：跳过且不重编号
		}
		c.read = idx
		if rec.deleted {
			delete(c.view, rec.key)
		} else {
			c.view[rec.key] = rec.value
		}
		return rec.seq, rec.key, rec.value, rec.deleted, nil
	}
	c.read = idx
	return 0, "", "", false, ErrNoRecord
}

// Position 返回下一次读取起始序号。
func (c *Consumer) Position() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read
}

// View 返回消费者当前视图的快照副本，删除的键不会出现。
func (c *Consumer) View() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.view))
	for k, v := range c.view {
		out[k] = v
	}
	return out
}

// Name 返回消费者注册名。
func (c *Consumer) Name() string { return c.name }

// DumpView 返回消费者视图的确定性文本表示（按键排序）。
func (c *Consumer) DumpView() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.view))
	for k := range c.view {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	fmt.Fprintf(&b, "consumer[%s] readUpTo=%d view:", c.name, c.read)
	if len(keys) == 0 {
		b.WriteString(" <empty>")
		return b.String()
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "\n  %s=%q", k, c.view[k])
	}
	return b.String()
}
