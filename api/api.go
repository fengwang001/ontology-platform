// Package api 是增量内连接引擎的对外入口，仅依赖 djoin。
package api

import (
	"ontology/djoin"
	"ontology/rel"
)

// Row 是一条带符号的上游变更：Sign=+1 插入一次，Sign=-1 删除一次。
type Row struct {
	K    int64
	V    string
	Sign int
}

// Delta 是一条带符号的连接结果差分，下游把 Mult 加到 (K,A,B) 的多重性上。
type Delta struct {
	K    int64
	A, B string
	Mult int64
}

// 三类可判定哨兵错误（与 djoin 同一实例，errors.Is 可判）。
var (
	ErrDeleteMissing = djoin.ErrDeleteMissing
	ErrInvalidChange = djoin.ErrInvalidChange
	ErrViewLimit     = djoin.ErrViewLimit
)

// Joiner 是对外的增量内连接器，并发安全。
type Joiner struct {
	e *djoin.Engine
}

// New 创建一个物化结果上限为 maxView 个非零不同元组的连接器。
func New(maxView int) *Joiner {
	return &Joiner{e: djoin.New(maxView)}
}

// Feed 原子地吃一批（可同时改 R、S 两张表），
// 返回按 (K,A,B) 升序、每元组至多一条的非零差分；整批被拒时返回错误且不留痕。
func (j *Joiner) Feed(dR, dS []Row) ([]Delta, error) {
	out, err := j.e.Feed(conv(dR), conv(dS))
	if err != nil {
		return nil, err
	}
	ds := make([]Delta, len(out))
	for i, x := range out {
		ds[i] = Delta{K: x.K, A: x.A, B: x.B, Mult: x.Mult}
	}
	return ds, nil
}

// View 返回当前物化结果（某个完整批次之后的快照），按 (K,A,B) 升序。
func (j *Joiner) View() []Delta {
	in := j.e.View()
	out := make([]Delta, len(in))
	for i, x := range in {
		out[i] = Delta{K: x.K, A: x.A, B: x.B, Mult: x.Mult}
	}
	return out
}

// SelfCheck 对内置批次序列核验四条不变量，全部通过返回 nil。
func (j *Joiner) SelfCheck() error { return j.e.SelfCheck() }

func conv(in []Row) []rel.Row {
	out := make([]rel.Row, len(in))
	for i, x := range in {
		out[i] = rel.Row{K: x.K, V: x.V, Sign: x.Sign}
	}
	return out
}
