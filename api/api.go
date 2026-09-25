// Package api 是对象池的对外接口。依赖 opool。
package api

import (
	"errors"
	"fmt"

	"ontology/blk"
	"ontology/opool"
)

// 对外可判定哨兵错误（与 opool 同一实例）。
var (
	ErrInvalidMaxIdle = opool.ErrInvalidMaxIdle
	ErrDoubleRelease  = opool.ErrDoubleRelease
	ErrUnknownBlock   = opool.ErrUnknownBlock
)

// Block 是对外不透明的块句柄。
type Block struct{ b *blk.Block }

func (bl Block) String() string { // 以 b<id> 形式展示，便于演示与比对
	if bl.b == nil {
		return "b?"
	}
	return fmt.Sprintf("b%d", bl.b.ID())
}

// Pool 是固定大小块的复用池。
type Pool struct{ p *opool.Pool }

// New 创建池，maxIdle >= 1，否则返回 ErrInvalidMaxIdle。
func New(maxIdle int) (*Pool, error) {
	p, err := opool.New(maxIdle)
	if err != nil {
		return nil, err
	}
	return &Pool{p: p}, nil
}

// Acquire 取一个块：空闲列表非空弹栈顶（LIFO），否则新建。
func (pl *Pool) Acquire() (Block, error) {
	b, err := pl.p.Acquire()
	if err != nil {
		return Block{}, err
	}
	return Block{b: b}, nil
}

// Release 归还块；重复归还、未知/已回收块均被拒绝且不留痕。
func (pl *Pool) Release(b Block) error { return pl.p.Release(b.b) }

// Idle 返回当前空闲块数；Total 返回存活块总数（in-use + idle）。
func (pl *Pool) Idle() int { return pl.p.Idle() }

func (pl *Pool) Total() int { return pl.p.Total() }

// naive 是朴素参照：LIFO 栈 + 计数，逐操作镜像。
type naive struct {
	maxIdle int
	stack   []Block
	total   int
}

func (n *naive) acquire() Block {
	if len(n.stack) > 0 {
		b := n.stack[len(n.stack)-1]
		n.stack = n.stack[:len(n.stack)-1]
		return b
	}
	n.total++
	return Block{} // 新建块身份由被测池决定，调用方不比对
}

func (n *naive) release(b Block) {
	if len(n.stack) < n.maxIdle {
		n.stack = append(n.stack, b)
		return
	}
	n.total-- // 满池驱逐
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (pl *Pool) SelfCheck() error {
	p, err := New(2)
	if err != nil {
		return err
	}
	fail := func(msg string) error { return errors.New("selfcheck: " + msg) }
	// 不变量3+2：八步序列，LIFO 与满池驱逐
	var got [3]Block
	got[0], _ = p.Acquire()
	got[1], _ = p.Acquire()
	got[2], _ = p.Acquire()
	for _, b := range got {
		if err := p.Release(b); err != nil {
			return err
		}
	}
	if p.Idle() != 2 || p.Total() != 2 {
		return fail("满池驱逐后 Idle/Total 应为 2/2")
	}
	a7, _ := p.Acquire()
	a8, _ := p.Acquire()
	if a7 != got[1] || a8 != got[0] {
		return fail("LIFO 复用顺序应为 b1,b0")
	}
	// 不变量4：失败不留痕
	if _, err := New(0); !errors.Is(err, ErrInvalidMaxIdle) {
		return fail("New(0) 应返回 ErrInvalidMaxIdle")
	}
	i0, t0 := p.Idle(), p.Total()
	if err := p.Release(got[2]); !errors.Is(err, ErrUnknownBlock) {
		return fail("释放已回收块应返回 ErrUnknownBlock")
	}
	if p.Idle() != i0 || p.Total() != t0 {
		return fail("被拒操作改变了状态")
	}
	// 不变量1+2：与朴素参照逐步比对（含守恒 in-use+idle==Total）
	p2, _ := New(3)
	n := &naive{maxIdle: 3}
	held := map[Block]bool{}
	ops := []int{0, 0, 0, 1, 1, 0, 1, 0, 0, 1, 1, 1, 0, 1} // 0=Acquire 1=Release
	var fifo []Block
	for _, op := range ops {
		if op == 0 || len(fifo) == 0 {
			b, err := p2.Acquire()
			if err != nil {
				return err
			}
			if exp := n.acquire(); exp.b != nil && exp != b {
				return fail("复用顺序与朴素 LIFO 参照不一致")
			}
			held[b], fifo = true, append(fifo, b)
		} else {
			b := fifo[0]
			fifo = fifo[1:]
			delete(held, b)
			if err := p2.Release(b); err != nil {
				return err
			}
			n.release(b)
		}
		if p2.Idle() != len(n.stack) || p2.Total() != n.total ||
			len(held)+p2.Idle() != p2.Total() || p2.Idle() > 3 {
			return fail("守恒/参照不变量被破坏")
		}
	}
	return nil
}
