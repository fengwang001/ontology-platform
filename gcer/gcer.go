// Package gcer 实现撤回日志与快照管理：Append/Open/Close/Replay/GC，
// 活跃快照最小水位用小顶堆维护，回收上界 G 单调不减。依赖 rlg。
package gcer

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/rlg"
)

// 可判定哨兵错误，四类互不相同。
var (
	ErrOutOfRange       = errors.New("gcer: replay seq beyond snapshot watermark")
	ErrReclaimed        = errors.New("gcer: replay seq already reclaimed")
	ErrNoSuchSnapshot   = errors.New("gcer: no such snapshot")
	ErrTooManySnapshots = errors.New("gcer: too many active snapshots")
)

// wmHeap 是水位的最小堆。Close 采用懒删除：只减计数，过期堆顶在取最小值时弹出。
type wmHeap []int64

func (h wmHeap) Len() int           { return len(h) }
func (h wmHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h wmHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *wmHeap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *wmHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// Log 是撤回日志。全部状态在进程内存，一把互斥锁保证并发安全。
type Log struct {
	mu       sync.Mutex
	recs     map[int64]rlg.Record // Seq -> 记录，GC 后删除
	seq      int64                // 当前最大 Seq
	snaps    map[int64]int64      // 快照 id -> 水位
	nextID   int64
	counts   map[int64]int // 水位 -> 该水位上的活跃快照数
	h        wmHeap
	wm       int64 // 回收上界 G，单调不减
	maxSnaps int
	checked  int // 非导出：最近一次 GC/Close 确定最小水位时检查过的快照数
}

// New 创建日志，maxSnapshots 为活跃快照上限。
func New(maxSnapshots int) *Log {
	return &Log{
		recs:     make(map[int64]rlg.Record),
		snaps:    make(map[int64]int64),
		counts:   make(map[int64]int),
		maxSnaps: maxSnapshots,
	}
}

// Append 追加一条撤回记录，Seq 从 1 连续递增，返回新 Seq。
func (l *Log) Append(key, oldVal string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	l.recs[l.seq] = rlg.Record{Seq: l.seq, Key: key, OldVal: oldVal}
	return l.seq
}

// Open 打开读快照，水位 = 当前 Seq。超过 maxSnapshots 拒绝且不留痕。
func (l *Log) Open() (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.snaps) >= l.maxSnaps {
		return 0, ErrTooManySnapshots
	}
	l.nextID++
	l.snaps[l.nextID] = l.seq
	l.counts[l.seq]++
	heap.Push(&l.h, l.seq)
	return l.nextID, nil
}

// Close 关闭快照并释放水位；快照不存在则拒绝且不留痕。关闭后重估最小水位。
func (l *Log) Close(id int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.snaps[id]
	if !ok {
		return ErrNoSuchSnapshot
	}
	delete(l.snaps, id)
	l.counts[w]--
	if l.counts[w] == 0 {
		delete(l.counts, w)
	}
	l.minWatermark()
	return nil
}

// Replay 在快照 id 上重放位点 seq。越界与已回收分别报不同哨兵错误。
func (l *Log) Replay(id, seq int64) (key, oldVal string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.snaps[id]
	if !ok {
		return "", "", ErrNoSuchSnapshot
	}
	if seq > w {
		return "", "", ErrOutOfRange
	}
	if seq <= l.wm {
		return "", "", ErrReclaimed
	}
	r, ok := l.recs[seq]
	if !ok {
		return "", "", ErrReclaimed
	}
	return r.Key, r.OldVal, nil
}

// GC 回收所有 Seq<=G 的记录，G=最小活跃水位（无活跃快照时 G=当前 Seq）。G 只前进。
func (l *Log) GC() {
	l.mu.Lock()
	defer l.mu.Unlock()
	g := l.minWatermark()
	if g <= l.wm {
		return
	}
	for s := l.wm + 1; s <= g; s++ {
		delete(l.recs, s)
	}
	l.wm = g
}

// Watermark 返回当前回收上界 G。
func (l *Log) Watermark() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.wm
}

// minWatermark 取最小活跃水位：堆顶 O(1)，懒弹出已关闭的过期堆顶。
// 无活跃快照时返回当前 Seq。checked 记录本次检查过的快照数。调用方须持锁。
func (l *Log) minWatermark() int64 {
	l.checked = 0
	for l.h.Len() > 0 {
		l.checked++
		top := l.h[0]
		if l.counts[top] > 0 {
			return top
		}
		heap.Pop(&l.h)
	}
	return l.seq
}
