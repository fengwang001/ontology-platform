// Package pipe 负责流水线构建、下推规划与按优化后链执行。
package pipe

import (
	"errors"
	"fmt"
	"slices"

	"ontology/pred"
)

// ErrBarrier 下推非法：请求把算子移动越过状态过滤器（屏障）。
var ErrBarrier = errors.New("pipe: pushdown crosses stateful barrier")

// Op 是流水线算子：无状态过滤器，或状态过滤器（屏障）。
type Op struct {
	sl *pred.Stateless // 非 nil：无状态过滤器
	hw *pred.HighWater // 非 nil：状态过滤器
}

// Filter 用无状态谓词构造过滤器。
func Filter(p pred.Stateless) Op { return Op{sl: &p} }

// RecordHigh 构造状态过滤器「记录新高」（题面 S）。
func RecordHigh() Op { return Op{hw: pred.NewHighWater()} }

// Stateful 报告算子是否为状态过滤器。
func (o Op) Stateful() bool { return o.hw != nil }

func (o Op) eval(e pred.Event) bool {
	if o.Stateful() {
		return o.hw.Eval(e)
	}
	return o.sl.Eval(e)
}

// Move 是一次下推请求：把（当前链中）位置 From 的算子移动到位置 To。
type Move struct{ From, To int }

// Plan 是优化后的执行计划。
type Plan struct {
	chain  []Op
	checks int // 非导出计数器：本次构建执行的相邻可交换性检查次数
}

// Build 构建下推计划：先校验全部谓词与全部移动请求（任一非法即整体失败、
// 不产出计划），再单遍扫描把同一段内的无状态过滤器合并为 AND 谓词。
// moves 按给定顺序依次应用；任何跨越状态过滤器的移动都被拒绝。
func Build(ops []Op, moves ...Move) (*Plan, error) {
	chain := slices.Clone(ops)
	for _, o := range chain {
		if !o.Stateful() {
			if err := o.sl.Valid(); err != nil {
				return nil, err
			}
		}
	}
	for _, mv := range moves {
		if mv.From < 0 || mv.From >= len(chain) || mv.To < 0 || mv.To >= len(chain) {
			return nil, fmt.Errorf("%w: move %d->%d out of range", ErrBarrier, mv.From, mv.To)
		}
		if chain[mv.From].Stateful() {
			return nil, fmt.Errorf("%w: stateful filter at %d cannot move", ErrBarrier, mv.From)
		}
		lo, hi := min(mv.From, mv.To), max(mv.From, mv.To)
		for i := lo; i <= hi; i++ {
			if i != mv.From && chain[i].Stateful() {
				return nil, fmt.Errorf("%w: move %d->%d crosses stateful filter at %d", ErrBarrier, mv.From, mv.To, i)
			}
		}
		o := chain[mv.From]
		chain = slices.Delete(chain, mv.From, mv.From+1)
		chain = slices.Insert(chain, mv.To, o)
	}
	// 单遍扫描：每对相邻算子恰好做一次可交换性检查，同段无状态合并。
	var out []Op
	checks := 0
	for i := 0; i < len(chain); i++ {
		if chain[i].Stateful() {
			if i+1 < len(chain) {
				checks++ // 相邻对 (stateful, next)：不可交换
			}
			out = append(out, chain[i])
			continue
		}
		ps := []pred.Stateless{*chain[i].sl}
		for i+1 < len(chain) {
			checks++ // 相邻对 (stateless, next)
			if chain[i+1].Stateful() {
				break
			}
			i++
			ps = append(ps, *chain[i].sl)
		}
		out = append(out, Filter(pred.And(ps...)))
	}
	return &Plan{chain: out, checks: checks}, nil
}

// Len 返回优化后链的算子数（计划结构信息，非计数器）。
func (p *Plan) Len() int { return len(p.chain) }

// Run 按优化后的链执行事件：逐事件依次经过每个算子，短路。
func (p *Plan) Run(evs []pred.Event) []pred.Event {
	return runChain(p.chain, evs)
}

// RunNaive 按原始链顺序逐算子求值，不做任何重排/合并（朴素基线）。
func RunNaive(ops []Op, evs []pred.Event) []pred.Event {
	return runChain(ops, evs)
}

func runChain(chain []Op, evs []pred.Event) []pred.Event {
	var out []pred.Event
	for _, e := range evs {
		ok := true
		for _, o := range chain {
			if !o.eval(e) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, e)
		}
	}
	return out
}
