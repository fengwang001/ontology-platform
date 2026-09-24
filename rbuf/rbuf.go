// Package rbuf 事务重排缓冲：内存行 + 溢写块号列表，超限按 (内存行数,事务号) 用堆选溢写对象。
package rbuf

import (
	"container/heap"
	"errors"
	"fmt"
	"math/bits"
	"sync"

	"ontology/spill"
)

var ErrNoTx = errors.New("rbuf: 事务号不存在") // 四类哨兵错误互不相同
var ErrDupTx = errors.New("rbuf: 事务号重复")
var ErrSpillFull = errors.New("rbuf: 溢写存储已满")

type txState struct {
	tx, hi int      // 事务号、堆下标
	rows   []string // 内存行
	blocks []int    // 溢写块号（升序）
}

type txHeap struct {
	s   []*txState
	cnt *int
}

func (h txHeap) Len() int { return len(h.s) }

func (h txHeap) Less(i, j int) bool {
	*h.cnt++
	x, y := h.s[i], h.s[j]
	return len(x.rows) > len(y.rows) || len(x.rows) == len(y.rows) && x.tx < y.tx
}
func (h txHeap) Swap(i, j int) {
	*h.cnt++
	h.s[i], h.s[j] = h.s[j], h.s[i]
	h.s[i].hi, h.s[j].hi = i, j
}
func (h *txHeap) Push(x any) { t := x.(*txState); t.hi = len(h.s); h.s = append(h.s, t) }
func (h *txHeap) Pop() any   { t := h.s[len(h.s)-1]; h.s = h.s[:len(h.s)-1]; return t }

// Buffer 是事务重排缓冲，所有方法可并发调用。
type Buffer struct {
	mu      sync.Mutex
	limit   int
	store   *spill.Store
	txs     map[int]*txState
	h       txHeap
	m       int // 未结束事务内存行数之和
	log     []string
	visited int // 非导出：最近一次 Append 的堆条目访问数
}

func New(memLimit int, store *spill.Store) *Buffer {
	b := &Buffer{limit: memLimit, store: store, txs: map[int]*txState{}}
	b.h.cnt = &b.visited
	return b
}

func (b *Buffer) Begin(tx int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.txs[tx]; ok {
		return ErrDupTx
	}
	t := &txState{tx: tx}
	b.txs[tx] = t
	heap.Push(&b.h, t)
	return nil
}

func (b *Buffer) Append(tx int, row string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.visited = 0
	t := b.txs[tx]
	if t == nil {
		return ErrNoTx
	}
	if b.m+1 > b.limit && b.store.Full() { // 追加后必超限且已满：整体拒绝
		return ErrSpillFull
	}
	t.rows, b.m = append(t.rows, row), b.m+1
	heap.Fix(&b.h, t.hi)
	if b.m > b.limit {
		b.visited++ // 访问堆顶 = 选出的溢写对象
		v := b.h.s[0]
		v.blocks = append(v.blocks, b.store.Put(v.tx, v.rows))
		b.m -= len(v.rows)
		v.rows = nil
		heap.Fix(&b.h, v.hi)
	}
	return nil
}

func (b *Buffer) end(tx int, commit bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.txs[tx]
	if t == nil {
		return ErrNoTx
	}
	if commit {
		for _, no := range t.blocks {
			b.log = append(b.log, b.store.Get(no)...)
		}
		b.log = append(b.log, t.rows...)
	}
	b.store.DeleteTx(tx)
	b.m -= len(t.rows)
	heap.Remove(&b.h, t.hi)
	delete(b.txs, tx)
	return nil
}
func (b *Buffer) Commit(tx int) error   { return b.end(tx, true) }
func (b *Buffer) Rollback(tx int) error { return b.end(tx, false) }
func (b *Buffer) Log() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.log...)
}
func (b *Buffer) Blocks() []int { b.mu.Lock(); defer b.mu.Unlock(); return b.store.Blocks() }
func (b *Buffer) M() int        { b.mu.Lock(); defer b.mu.Unlock(); return b.m }

// CheckSpillSelect 核验溢写 Append 的堆访问数 ≤ 8·⌈log2 m⌉+8（只报结论）。
func CheckSpillSelect() error {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		b := New(2*m+1, spill.New(m+1)) // limit 足够大，setup 不溢写
		for tx := 1; tx <= m; tx++ {
			if err := b.Begin(tx); err != nil {
				return err
			}
			for r := 0; r < tx%3+1; r++ { // 各事务行数不同（1~3）
				if err := b.Append(tx, "r"); err != nil {
					return err
				}
			}
		}
		b.limit = b.m // 让下一次 Append 恰好触发溢写
		if err := b.Append(1, "x"); err != nil {
			return err
		}
		if log2 := bits.Len(uint(m - 1)); b.visited > 8*log2+8 {
			return fmt.Errorf("rbuf: m=%d 堆访问数超上界", m)
		}
	}
	return nil
}
