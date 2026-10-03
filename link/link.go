// Package link 管理全互联拓扑中一条有向链路的 FIFO 复制队列、
// 积压字节预算与投递失败/重试/失败转移。
package link

import (
	"sync"

	"ontology/region"
)

// Item 是链路上的一个复制项，携带完整版本与其投递重试计数。
type Item struct {
	Ver   region.Version
	Tries int
}

// SendFunc 是注入的投递函数。
type SendFunc func(item Item) (applied bool, err error)

// FailError 是调用方可用于模拟投递失败的哨兵错误。
type FailError struct{}

func (FailError) Error() string { return "link: send failed" }

// Link 是一条 src->dst 有向链路。
type Link struct {
	mu        sync.Mutex
	src, dst  string
	c         int64
	r         int
	queue     []Item
	failed    map[region.VersionID]region.Version
	backlog   int64
	enqueued  int64
	delivered int64
	transfers int64
	lost      int64
	dup       int64
}

// New 创建一条容量 C、重试上限 R 的链路。
func New(src, dst string, c int64, r int) *Link {
	return &Link{
		src:    src,
		dst:    dst,
		c:      c,
		r:      r,
		failed: make(map[region.VersionID]region.Version),
	}
}

// Src 返回源区域名。
func (l *Link) Src() string { return l.src }

// Dst 返回目的区域名。
func (l *Link) Dst() string { return l.dst }

// Backlog 返回当前积压字节。
func (l *Link) Backlog() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.backlog
}

// CanEnqueue 报告追加 size 字节是否不超容量（恰等通过）。
func (l *Link) CanEnqueue(size int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.backlog+size <= l.c
}

// Enqueue 把复制项加入队尾（调用方须先用 CanEnqueue 检查）。
func (l *Link) Enqueue(v region.Version) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queue = append(l.queue, Item{Ver: v})
	l.backlog += v.Size
	l.enqueued++
}

// MarkLost 记录本链路因 Relaxed 超预算而丢弃一版。
func (l *Link) MarkLost() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lost++
}

// Peek 返回队首项及其是否存在。
func (l *Link) Peek() (Item, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.queue) == 0 {
		return Item{}, false
	}
	return l.queue[0], true
}

// PopFront 移除队首、释放积压字节并计一次已投递（err==nil）。
func (l *Link) PopFront() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.queue) == 0 {
		return
	}
	l.backlog -= l.queue[0].Ver.Size
	l.delivered++
	l.queue = l.queue[1:]
}

// RecordTrial 对队首项记录一次 Send 调用，返回加后的重试计数。
func (l *Link) RecordTrial() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.queue) == 0 {
		return 0
	}
	l.queue[0].Tries++
	return l.queue[0].Tries
}

// TransferHeadToFailed 把队首项转入失败列表、释放积压、计一次失败转移。
func (l *Link) TransferHeadToFailed() region.Version {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.queue) == 0 {
		return region.Version{}
	}
	head := l.queue[0]
	l.queue = l.queue[1:]
	l.backlog -= head.Ver.Size
	l.transfers++
	l.failed[head.Ver.ID] = head.Ver
	return head.Ver
}

// Requeue 把失败项从失败列表移除、清零重试计数后放回队尾
// （调用方须先做 CanEnqueue 预算检查）。
func (l *Link) Requeue(v region.Version) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failed, v.ID)
	l.queue = append(l.queue, Item{Ver: v})
	l.backlog += v.Size
	l.enqueued++
}

// TakeFailed 取出并移除失败项；不存在返回 false。
func (l *Link) TakeFailed(id region.VersionID) (region.Version, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.failed[id]
	if !ok {
		return region.Version{}, false
	}
	delete(l.failed, id)
	return v, true
}

// PeekFailed 查看失败项但不移除。
func (l *Link) PeekFailed(id region.VersionID) (region.Version, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.failed[id]
	return v, ok
}

// Len 返回在队项数。
func (l *Link) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.queue)
}

// Failed 返回失败列表快照（按 origin/seq 排序，保证可重放）。
func (l *Link) Failed() []region.Version {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]region.Version, 0, len(l.failed))
	for _, v := range l.failed {
		out = append(out, v)
	}
	sortVersions(out)
	return out
}

// Queue 返回在队项快照（FIFO 顺序）。
func (l *Link) Queue() []Item {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Item, len(l.queue))
	copy(out, l.queue)
	return out
}

// Lost 返回 Relaxed 丢失计数。
func (l *Link) Lost() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lost
}

// Dup 返回重复确认计数。
func (l *Link) Dup() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dup
}

// AddDup 累加一次重复。
func (l *Link) AddDup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dup++
}

// Counts 返回 入队总数/已投递/失败转移累计/在队数/积压字节，
// 恒满足 入队总数 = 已投递 + 失败转移 + 在队数。
func (l *Link) Counts() (enqueued, delivered, transfers, queued int64, backlog int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enqueued, l.delivered, l.transfers, int64(len(l.queue)), l.backlog
}

func sortVersions(vs []region.Version) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && lessVersion(vs[j], vs[j-1]); j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}

func lessVersion(a, b region.Version) bool {
	if a.ID.Origin != b.ID.Origin {
		return a.ID.Origin < b.ID.Origin
	}
	return a.ID.Seq < b.ID.Seq
}
