// Package api 对外接口：事务重排缓冲的创建、操作、日志与自检。
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/rbuf"
	"ontology/spill"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrParam     = errors.New("api: 参数非法")
	ErrNoTx      = rbuf.ErrNoTx
	ErrDupTx     = rbuf.ErrDupTx
	ErrSpillFull = rbuf.ErrSpillFull
)

// API 是事务重排缓冲的对外句柄，可并发使用。
type API struct{ b *rbuf.Buffer }

// New 创建缓冲；memLimit 或 maxSpill 小于 1 返回 ErrParam。
func New(memLimit, maxSpill int) (*API, error) {
	if memLimit < 1 || maxSpill < 1 {
		return nil, ErrParam
	}
	return &API{b: rbuf.New(memLimit, spill.New(maxSpill))}, nil
}

func (a *API) Begin(tx int) error              { return a.b.Begin(tx) }
func (a *API) Append(tx int, row string) error { return a.b.Append(tx, row) }
func (a *API) Commit(tx int) error             { return a.b.Commit(tx) }
func (a *API) Rollback(tx int) error           { return a.b.Rollback(tx) }

// Log 返回下游日志副本；SpillBlocks 返回存储中当前块号（升序）。
func (a *API) Log() []string      { return a.b.Log() }
func (a *API) SpillBlocks() []int { return a.b.Blocks() }

// SelfCheck 对内置多组随机序列核验 I1（朴素一致）、I3（M 与块数有界）与 I4（失败不留痕）。
func (a *API) SelfCheck() error {
	for _, tc := range []struct{ l, s, seed, n int }{{4, 2, 1, 2000}, {1, 1, 7, 1000}, {9, 4, 3, 3000}} {
		if err := vsNaive(tc.l, tc.s, tc.seed, tc.n); err != nil {
			return err
		}
	}
	return rejectNoTrace()
}

type naive struct {
	open map[int][]string
	log  []string
}

func vsNaive(limit, maxSpill, seed, nops int) error {
	a, _ := New(limit, maxSpill)
	n := &naive{open: map[int][]string{}}
	r := rand.New(rand.NewSource(int64(seed)))
	for i := 0; i < nops; i++ {
		tx := r.Intn(6) + 1
		var e error
		switch r.Intn(4) {
		case 0:
			_, dup := n.open[tx]
			if e = a.Begin(tx); (e == nil) == dup {
				return fmt.Errorf("selfcheck: Begin(%d) 接受性不一致", tx)
			}
			if !dup {
				n.open[tx] = nil
			}
		case 1, 2:
			row := fmt.Sprintf("t%d-%d", tx, i)
			if e = a.Append(tx, row); e == nil {
				if _, ok := n.open[tx]; !ok {
					return fmt.Errorf("selfcheck: Append(%d) 参照无事务却接受", tx)
				}
				n.open[tx] = append(n.open[tx], row)
			} else if !errors.Is(e, ErrNoTx) && !errors.Is(e, ErrSpillFull) {
				return fmt.Errorf("selfcheck: Append(%d) 意外错误 %v", tx, e)
			}
		case 3:
			commit := r.Intn(2) == 0
			if commit {
				e = a.Commit(tx)
			} else {
				e = a.Rollback(tx)
			}
			rows, ok := n.open[tx]
			if (e == nil) != ok {
				return fmt.Errorf("selfcheck: 结束(%d) 接受性不一致", tx)
			}
			if ok {
				delete(n.open, tx)
				if commit {
					n.log = append(n.log, rows...)
				}
			}
		}
		if a.b.M() > limit || len(a.SpillBlocks()) > maxSpill {
			return fmt.Errorf("selfcheck: 内存或块数越界")
		}
	}
	if len(a.Log()) != len(n.log) {
		return fmt.Errorf("selfcheck: 下游日志与朴素参照长度不一致")
	}
	for i := range n.log {
		if a.Log()[i] != n.log[i] {
			return fmt.Errorf("selfcheck: 下游日志与朴素参照不一致 @%d", i)
		}
	}
	return nil
}

// rejectNoTrace 核验 I4：四类错误可判定、互不相同，被拒后状态不变且腾块后可重试。
func rejectNoTrace() error {
	if _, e := New(0, 1); !errors.Is(e, ErrParam) {
		return fmt.Errorf("selfcheck: 参数非法未返回 ErrParam")
	}
	if _, e := New(1, 0); !errors.Is(e, ErrParam) {
		return fmt.Errorf("selfcheck: maxSpill 非法未返回 ErrParam")
	}
	a, _ := New(1, 1)
	if e := a.Append(9, "x"); !errors.Is(e, ErrNoTx) {
		return fmt.Errorf("selfcheck: Append 未开事务应 ErrNoTx")
	}
	if e := a.Commit(9); !errors.Is(e, ErrNoTx) || errors.Is(a.Rollback(9), nil) {
		return fmt.Errorf("selfcheck: 结束未开事务应 ErrNoTx")
	}
	a.Begin(1)
	if e := a.Begin(1); !errors.Is(e, ErrDupTx) {
		return fmt.Errorf("selfcheck: 重复 Begin 应 ErrDupTx")
	}
	a.Append(1, "x")
	a.Append(1, "y") // M=2>1，tx1 整块溢写为 #0
	a.Begin(2)
	a.Append(2, "p")
	lb, bb, mb := len(a.Log()), len(a.SpillBlocks()), a.b.M()
	if e := a.Append(2, "q"); !errors.Is(e, ErrSpillFull) {
		return fmt.Errorf("selfcheck: 溢写满应 ErrSpillFull: %v", e)
	}
	if len(a.Log()) != lb || len(a.SpillBlocks()) != bb || a.b.M() != mb {
		return fmt.Errorf("selfcheck: 被拒 Append 改变了状态")
	}
	a.Commit(1) // 腾出 #0
	if e := a.Append(2, "q"); e != nil {
		return fmt.Errorf("selfcheck: 腾块后重试应成功: %v", e)
	}
	return nil
}
