// Package buf 是不依赖其他包的 FIFO 变更缓冲：入队、高水位判定、
// 批量/延迟两档触发判定，并按触发从头部取出一批。
package buf

// Entry 是下游 Sink 能看到的条目（不含逻辑时钟）。
type Entry struct {
	Key string
	Val int
}

// item 是缓冲内部条目，额外记录入队时的逻辑时钟。
type item struct {
	entry   Entry
	arrival int64
}

// Kind 标识一次 Tick 排水中 flush 的触发类型。
type Kind int

const (
	None      Kind = iota // 两档触发均不成立
	BySize                // 批量触发：len(buffer) >= B
	ByLatency             // 延迟触发：最旧条目 age >= L
)

// Lease 是一次取出的不透明凭证；flush 失败时凭它把整批按原顺序放回头部。
type Lease struct {
	items []item
}

// Buffer 是 FIFO 缓冲。scan 为非导出计数器（见复杂度约束），
// 不出现在任何导出接口中。
type Buffer struct {
	b, h, l int
	data    []item
	scan    int
}

// New 创建容量参数为 B/H/L 的空缓冲；参数合法性由上层保证。
func New(b, h, l int) *Buffer { return &Buffer{b: b, h: h, l: l} }

// Len 返回当前缓冲条目数。
func (q *Buffer) Len() int { return len(q.data) }

// Full 报告是否已达高水位（len >= H）。
func (q *Buffer) Full() bool { return len(q.data) >= q.h }

// Push 在尾部入队一条；调用方需先判 Full 与空 key。
func (q *Buffer) Push(key string, val int, now int64) {
	q.data = append(q.data, item{entry: Entry{Key: key, Val: val}, arrival: now})
}

// take 从头部取出恰好 n 条（n 超出长度时取全部），逐条访问并计数，
// scan 只统计本次为确定并取出 flush 集合而访问的条目，与总长度无关。
func (q *Buffer) take(n int) ([]Entry, *Lease) {
	if n > len(q.data) {
		n = len(q.data)
	}
	q.scan = n
	got := make([]item, n)
	copy(got, q.data[:n]) // 逐条访问头部 n 条即确定整个 flush 集合
	out := make([]Entry, n)
	for i := range got {
		out[i] = got[i].entry
	}
	q.data = q.data[n:]
	return out, &Lease{items: got}
}

// TakeTick 按 Tick 的两档规则（批量优先）尝试取出头部一批。
func (q *Buffer) TakeTick(now int64) (batch []Entry, lease *Lease, kind Kind) {
	switch {
	case len(q.data) >= q.b: // 批量触发优先
		batch, lease = q.take(q.b)
		return batch, lease, BySize
	case len(q.data) > 0 && now-q.data[0].arrival >= int64(q.l):
		batch, lease = q.take(len(q.data)) // 此时 len < B，整头部冲走
		return batch, lease, ByLatency
	default:
		return nil, nil, None
	}
}

// TakeFlush 强制取出头部一批（至多 B 条）；缓冲空时 fired=false。
func (q *Buffer) TakeFlush() (batch []Entry, lease *Lease, fired bool) {
	if len(q.data) == 0 {
		q.scan = 0
		return nil, nil, false
	}
	n := q.b
	if n > len(q.data) {
		n = len(q.data)
	}
	batch, lease = q.take(n)
	return batch, lease, true
}

// Return 把 Lease 代表的整批按原顺序放回缓冲头部（整体回滚）。
func (q *Buffer) Return(lease *Lease) {
	if lease == nil || len(lease.items) == 0 {
		return
	}
	q.data = append(lease.items, q.data...)
}
