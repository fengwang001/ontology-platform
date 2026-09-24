// Package fww 管理多个键的 FWW 寄存器：map[Key]*reg、变更日志产出、丢弃计数。
// 依赖 reg，不依赖 api。
package fww

import (
	"errors"

	"ontology/reg"
)

// 三类可判定故障，互不相同的哨兵错误。
var (
	ErrEmptyKey = errors.New("fww: empty key")
	ErrBadSeq   = errors.New("fww: seq must be >= 1")
	ErrDupSeq   = errors.New("fww: seq duplicates current effective seq")
)

// Write 是上游的一条写入。
type Write struct {
	Key string
	Seq int64
	Val string
}

// Change 是一条变更日志：+(Key,Seq,Val) 或 -(Key,Seq,Val)（Retract=true）。
type Change struct {
	Retract bool
	Key     string
	Seq     int64
	Val     string
}

// M 是多键寄存器表。不是并发安全的，并发封装在 api 层。
type M struct {
	regs    map[string]reg.Reg
	dropped int
	checked int // 最近一次写入检查过的键个数（非导出，不进公开接口）
}

func New() *M { return &M{regs: map[string]reg.Reg{}} }

// Feed 应用一批写入，返回产出的变更日志。
// 任一条被拒则整批不生效：先在克隆表上试算，全部成功才提交（不变量 4）。
func (m *M) Feed(ws []Write) ([]Change, error) {
	regs := make(map[string]reg.Reg, len(m.regs)+len(ws))
	for k, r := range m.regs {
		regs[k] = r
	}
	var out []Change
	dropped, checked := m.dropped, 0
	for _, w := range ws {
		if w.Key == "" {
			return nil, ErrEmptyKey
		}
		if w.Seq < 1 {
			return nil, ErrBadSeq
		}
		r := regs[w.Key] // 一次 map 定位，只检查这 1 个键，不整表扫描
		checked = 1
		if r.Set && w.Seq == r.Seq {
			return nil, ErrDupSeq
		}
		retract, oseq, oval, won := r.Apply(w.Seq, w.Val)
		if retract { // 撤回的必须恰好是已物化的那条：由 reg.Apply 原样返回
			out = append(out, Change{Retract: true, Key: w.Key, Seq: oseq, Val: oval})
		}
		if won {
			out = append(out, Change{Key: w.Key, Seq: w.Seq, Val: w.Val})
		} else {
			dropped++
		}
		regs[w.Key] = r
	}
	m.regs, m.dropped, m.checked = regs, dropped, checked
	return out, nil
}

// View 返回 Key→生效 Val 的物化视图。
func (m *M) View() map[string]string {
	v := make(map[string]string, len(m.regs))
	for k, r := range m.regs {
		v[k] = r.Val
	}
	return v
}

// Dropped 返回累积丢弃数。
func (m *M) Dropped() int { return m.dropped }
