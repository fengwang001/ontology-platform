// Package gcer 实现撤回日志与快照水位管理：Append/Open/Close/Replay/GC。
// 依赖 rlg；活跃快照水位用小顶堆（懒删除）维护，取最小 O(1)。
package gcer

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/rlg"
)

// 可判定哨兵错误，互不相同。
var (
	ErrOutOfRange      = errors.New("gcer: replay seq beyond snapshot watermark")
	ErrReclaimed       = errors.New("gcer: replay seq already reclaimed")
	ErrUnknownSnapshot = errors.New("gcer: snapshot not found")
	ErrTooManySnaps    = errors.New("gcer: snapshot count exceeds maxSnapshots")
)

type snapItem struct{ wm, id int64 }

// wmHeap 是按水位的小顶堆；Close 只做懒删除（map 删条目），
// 堆顶失效条目在取最小时弹出，故取最小摊还 O(1) 次检查。
type wmHeap []snapItem

func (h wmHeap) Len() int           { return len(h) }
func (h wmHeap) Less(i, j int) bool { return h[i].wm < h[j].wm }
func (h wmHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *wmHeap) Push(x any)        { *h = append(*h, x.(snapItem)) }
func (h *wmHeap) Pop() any { old := *h; *h = old[:len(old)-1]; return old[len(old)-1] }

// Collector 是撤回日志回收器。G 为回收上界（含等号：Seq<=G 已回收）。
type Collector struct {
	mu         sync.Mutex
	entries    map[int64]rlg.Entry
	snaps      map[int64]int64 // 快照 id -> 水位
	h          wmHeap
	seq        int64
	nextID     int64
	G          int64
	maxSnaps   int
	probeCount int // 非导出：最近一次 GC/Close 为确定最小水位检查的快照数
}

func New(maxSnapshots int) *Collector {
	return &Collector{
		entries:  make(map[int64]rlg.Entry),
		snaps:    make(map[int64]int64),
		maxSnaps: maxSnapshots,
	}
}

// Append 追加一条撤回记录，Seq 自动从 1 连续递增。
func (c *Collector) Append(key, oldVal string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	c.entries[c.seq] = rlg.Entry{Seq: c.seq, Key: key, OldVal: oldVal}
}

// Open 打开读快照，水位 = 当前 Seq。超限整体失败、不留痕。
func (c *Collector) Open() (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxSnaps > 0 && len(c.snaps) >= c.maxSnaps {
		return 0, ErrTooManySnaps
	}
	c.nextID++
	id := c.nextID
	c.snaps[id] = c.seq
	heap.Push(&c.h, snapItem{wm: c.seq, id: id})
	return id, nil
}

// Close 关闭快照并释放水位；随后重估最小水位（懒删堆顶失效项）。
func (c *Collector) Close(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.snaps[id]; !ok {
		return ErrUnknownSnapshot
	}
	delete(c.snaps, id)
	c.minLocked()
	return nil
}

// Replay 在快照 id 上重放位点 seq。先校验存在性与越界，再判已回收；
// 任何拒绝都不改变状态。
func (c *Collector) Replay(id, seq int64) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	wm, ok := c.snaps[id]
	if !ok {
		return "", "", ErrUnknownSnapshot
	}
	if seq < 1 || seq > wm {
		return "", "", ErrOutOfRange
	}
	if seq <= c.G {
		return "", "", ErrReclaimed
	}
	e := c.entries[seq]
	return e.Key, e.OldVal, nil
}

// GC 回收所有 Seq<=G 的记录：G=最小活跃水位，无活跃快照则 G=当前 Seq。
// G 只前进不回退。
func (c *Collector) GC() {
	c.mu.Lock()
	defer c.mu.Unlock()
	ng := c.gcTargetLocked()
	if ng > c.G {
		for s := c.G + 1; s <= ng; s++ {
			delete(c.entries, s)
		}
		c.G = ng
	}
}

// Watermark 返回当前回收上界 G。
func (c *Collector) Watermark() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.G
}

// gcTargetLocked 计算本次 GC 的目标上界（调用方持锁）。
func (c *Collector) gcTargetLocked() int64 {
	if wm, ok := c.minLocked(); ok {
		return wm
	}
	return c.seq
}

// minLocked 弹出堆顶失效项，返回最小活跃水位；检查数计入 probeCount。
func (c *Collector) minLocked() (int64, bool) {
	c.probeCount = 0
	for len(c.h) > 0 {
		c.probeCount++
		top := c.h[0]
		if wm, ok := c.snaps[top.id]; ok && wm == top.wm {
			return top.wm, true
		}
		heap.Pop(&c.h)
	}
	return 0, false
}
