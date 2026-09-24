// Package api 是对外接口层。依赖 regroup。
package api

import (
	"fmt"
	"slices"

	"ontology/regroup"
	"ontology/txn"
)

// 对外类型与哨兵错误别名。
type (
	Event = txn.Event
	Txn   = txn.Txn
)

const (
	Begin    = txn.Begin
	Row      = txn.Row
	Commit   = txn.Commit
	Rollback = txn.Rollback
)

var (
	ErrInvalidEvent   = txn.ErrInvalidEvent
	ErrDuplicateBegin = txn.ErrDuplicateBegin
	ErrUnknownTx      = txn.ErrUnknownTx
	ErrBufferFull     = txn.ErrBufferFull
)

// Replayer 是 CDC 事务边界重组器的对外句柄，可并发使用。
type Replayer struct {
	r *regroup.R
}

// New 创建重组器，maxRows 为全部进行中事务缓冲行数合计的上限。
func New(maxRows int) *Replayer { return &Replayer{r: regroup.New(maxRows)} }

// Feed 处理一条事件，返回其触发的输出（可能为空）。
func (p *Replayer) Feed(ev Event) ([]Txn, error) { return p.r.Feed(ev) }

// Output 返回迄今输出的全部事务（按 COMMIT 顺序）。
func (p *Replayer) Output() []Txn { return p.r.Output() }

// Buffered 返回当前缓冲行数合计。
func (p *Replayer) Buffered() int { return p.r.Buffered() }

// naive 是朴素参照：保留全部被接受事件，COMMIT 时从头扫描取该 tx 的行。
func naive(evs []Event, maxRows int) []Txn {
	type st struct {
		accepted []Event
		active   map[int64]bool
		closed   map[int64]bool
		total    int
	}
	s := st{active: map[int64]bool{}, closed: map[int64]bool{}}
	var out []Txn
	for _, ev := range evs {
		if ev.Validate() != nil {
			continue
		}
		switch ev.Kind {
		case Begin:
			if s.active[ev.Tx] || s.closed[ev.Tx] {
				continue
			}
			s.active[ev.Tx] = true
		case Row:
			if !s.active[ev.Tx] || s.total+1 > maxRows {
				continue
			}
			s.total++
		case Commit:
			if !s.active[ev.Tx] {
				continue
			}
			var rows []string
			for _, a := range s.accepted {
				if a.Kind == Row && a.Tx == ev.Tx {
					rows = append(rows, a.Data)
				}
			}
			s.total -= len(rows)
			out = append(out, Txn{Tx: ev.Tx, Rows: rows})
			delete(s.active, ev.Tx)
			s.closed[ev.Tx] = true
		case Rollback:
			if !s.active[ev.Tx] {
				continue
			}
			for _, a := range s.accepted {
				if a.Kind == Row && a.Tx == ev.Tx {
					s.total--
				}
			}
			delete(s.active, ev.Tx)
			s.closed[ev.Tx] = true
		}
		s.accepted = append(s.accepted, ev)
	}
	return out
}

func ev(k txn.Kind, tx int64, d string) Event { return Event{Kind: k, Tx: tx, Data: d} }

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil。
// 不触碰接收者状态，可并发调用。
func (p *Replayer) SelfCheck() error {
	seqs := [][]Event{
		{ // 第三节十三步
			ev(Begin, 1, ""), ev(Begin, 2, ""), ev(Row, 2, "a"), ev(Row, 1, "b"),
			ev(Begin, 3, ""), ev(Row, 3, "c"), ev(Row, 2, "d"), ev(Commit, 2, ""),
			ev(Row, 1, "e"), ev(Rollback, 3, ""), ev(Row, 1, "f"), ev(Commit, 1, ""),
			ev(Commit, 3, ""),
		},
		{ // 空事务、孤儿 COMMIT、重复 BEGIN、非法事件、回滚行不出现
			ev(Begin, 5, ""), ev(Commit, 5, ""), ev(Commit, 5, ""), ev(Begin, 5, ""),
			ev(Commit, 9, ""), ev(Begin, 6, ""), ev(Row, 6, "x"), ev(Rollback, 6, ""),
			ev(Row, 6, "y"), {Kind: 99, Tx: 1}, {Tx: 0}, ev(Commit, 7, ""),
		},
	}
	for i, seq := range seqs {
		q := New(3)
		for _, ev := range seq {
			q.Feed(ev)
		}
		want := naive(seq, 3)
		got := q.Output()
		if len(got) != len(want) {
			return fmt.Errorf("seq %d: output len %d != naive %d", i, len(got), len(want))
		}
		seen := map[int64]bool{}
		for j, t := range got {
			if t.Tx != want[j].Tx || !slices.Equal(t.Rows, want[j].Rows) {
				return fmt.Errorf("seq %d: txn %d mismatch", i, j)
			}
			if seen[t.Tx] {
				return fmt.Errorf("seq %d: tx %d output twice", i, t.Tx)
			}
			seen[t.Tx] = true
		}
		if q.Buffered() != 0 { // 两序列结束时均无进行中事务
			return fmt.Errorf("seq %d: buffered %d != 0", i, q.Buffered())
		}
	}
	return nil
}
