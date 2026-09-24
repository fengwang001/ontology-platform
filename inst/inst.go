// Package inst 管理单个处理实例：已生效版本、当前规则集、按 tag 有序的缓冲区与刷出规则。
// 它只依赖 rule 包。
package inst

import (
	"errors"
	"sort"

	"ontology/rule"
)

type Data struct{ Key, Val int64 }

type Hit struct {
	Key    int64
	Val    int64
	RuleID string
	Ver    int
	Inst   int
}

var ErrBufferFull = errors.New("inst: buffer full")

type buffered struct {
	d   Data
	tag int
}

// Instance：缓冲区按 tag 不减排列，每项 tag 都大于已生效版本 v；lastScan 为非导出计数器。
type Instance struct {
	id       int
	v        int
	rules    rule.Set
	buf      []buffered
	maxBuf   int
	lastScan int
}

func New(id, maxBuffered int) *Instance {
	return &Instance{id: id, rules: rule.Set{}, maxBuf: maxBuffered}
}

func (in *Instance) V() int        { return in.v }
func (in *Instance) Buffered() int { return len(in.buf) }

// Apply 应用下一个版本（必须为 v+1），立即刷出 tag==新v 的缓冲，返回本次产生的命中。
func (in *Instance) Apply(u rule.Update) []Hit {
	in.v++
	in.rules.Apply(u)
	in.lastScan = 0
	t, made := in.v, []Hit{}
	for len(in.buf) > 0 { // 缓冲按 tag 不减且 tag>=t，只需看队头
		in.lastScan++
		if in.buf[0].tag != t {
			break
		}
		b := in.buf[0]
		in.buf = in.buf[1:]
		made = append(made, in.eval(b.d, t)...)
	}
	return made
}

// Receive：tag==v 立即处理并返回命中；tag>v 入缓冲；满则返回 ErrBufferFull 且不留痕。
func (in *Instance) Receive(d Data, tag int) ([]Hit, error) {
	if tag == in.v {
		return in.eval(d, tag), nil
	}
	if len(in.buf) >= in.maxBuf {
		return nil, ErrBufferFull
	}
	in.buf = append(in.buf, buffered{d: d, tag: tag}) // G 不减，追加即保持 tag 不减
	return nil, nil
}

func (in *Instance) eval(d Data, ver int) []Hit {
	hs := []Hit{}
	for _, id := range in.rules.Match(d.Val) {
		hs = append(hs, Hit{Key: d.Key, Val: d.Val, RuleID: id, Ver: ver, Inst: in.id})
	}
	return hs
}

// Invariant 核验 0<=v<=G、缓冲每项 tag>v 且按 tag 不减、不超上限。
func (in *Instance) Invariant(G int) bool {
	if in.v < 0 || in.v > G {
		return false
	}
	prev := in.v
	for _, b := range in.buf {
		if b.tag <= prev {
			return false
		}
		prev = b.tag
	}
	return len(in.buf) <= in.maxBuf
}

// SortHits 返回按 (Key,Val,RuleID,Ver,Inst) 排序后的命中副本。
func SortHits(z []Hit) []Hit {
	p := append([]Hit(nil), z...)
	sort.Slice(p, func(i, j int) bool {
		x, y := p[i], p[j]
		switch {
		case x.Key != y.Key:
			return x.Key < y.Key
		case x.Val != y.Val:
			return x.Val < y.Val
		case x.RuleID != y.RuleID:
			return x.RuleID < y.RuleID
		case x.Ver != y.Ver:
			return x.Ver < y.Ver
		default:
			return x.Inst < y.Inst
		}
	})
	return p
}

// ByInst 返回命中中属于实例 i 的那些，保持原有相对顺序。
func ByInst(h []Hit, i int) []Hit {
	p := []Hit{}
	for _, z := range h {
		if z.Inst == i {
			p = append(p, z)
		}
	}
	return p
}

// EqualSet 判断两段命中是否为同一多重集合（忽略顺序）。
func EqualSet(x, y []Hit) bool {
	p, q := SortHits(x), SortHits(y)
	if len(p) != len(q) {
		return false
	}
	for i := range p {
		if p[i] != q[i] {
			return false
		}
	}
	return true
}
