// Package pipe 实现流水线构建、谓词下推规划与按优化后链执行事件。依赖 pred。
package pipe

import (
	"errors"

	"ontology/pred"
)

// ErrBarrier 是下推非法错误：任何把算子移动过状态过滤器（屏障）的请求都被拒绝。
var ErrBarrier = errors.New("pipe: 下推非法：不得跨越状态过滤器")

// Op 是链上的一个算子：State 非 nil 时是状态过滤器（屏障），否则是无状态过滤器。
type Op struct {
	Name  string
	Pred  pred.Pred
	State *pred.RecordHigh
}

func (o Op) stateful() bool { return o.State != nil }

// step 是优化后链上的一步：一个（可能合并过的）无状态谓词，或一个状态过滤器。
type step struct {
	p     pred.Pred
	state *pred.RecordHigh
}

// Planner 构建下推计划。checks 记录最近一次 Plan 执行的
// 「相邻算子可交换性检查」次数——非导出，不出现在任何公开接口。
type Planner struct {
	checks int
}

// Plan 单遍扫描算子链：同一段（以状态过滤器为界）内的无状态过滤器
// 两两 AND 合并；状态过滤器原样保留、相对顺序不变。
// 每个算子只与左邻做一次可交换性检查，检查次数随链长线性增长。
func (pl *Planner) Plan(ops []Op) ([]step, error) {
	pl.checks = 0
	var steps []step
	var seg *pred.Pred // 当前段累积的短路 AND
	flush := func() {
		if seg != nil {
			steps = append(steps, step{p: *seg})
			seg = nil
		}
	}
	for i, o := range ops {
		if i > 0 {
			pl.checks++ // 与左邻算子的可交换性检查（每对相邻只查一次）
		}
		if o.stateful() {
			flush()
			steps = append(steps, step{state: o.State})
			continue
		}
		if err := o.Pred.Valid(); err != nil {
			return nil, err
		}
		if seg == nil {
			s := o.Pred
			seg = &s
		} else {
			m := pred.And(*seg, o.Pred)
			seg = &m
		}
	}
	flush()
	return steps, nil
}

// ValidateMove 校验「把 ops[from] 移动到 to 位置」的下推请求：
// 移动状态过滤器本身、或移动路径上存在状态过滤器，一律拒绝。
func ValidateMove(ops []Op, from, to int) error {
	if from < 0 || from >= len(ops) || to < 0 || to >= len(ops) {
		return ErrBarrier
	}
	if ops[from].stateful() && from != to {
		return ErrBarrier
	}
	lo, hi := min(from, to), max(from, to)
	for i := lo; i <= hi; i++ {
		if i != from && ops[i].stateful() {
			return ErrBarrier
		}
	}
	return nil
}

// Pipeline 是按优化后（或朴素）链执行事件的执行器。
type Pipeline struct {
	steps []step
}

// Build 用规划器构建优化后的流水线。
func (pl *Planner) Build(ops []Op) (*Pipeline, error) {
	steps, err := pl.Plan(ops)
	if err != nil {
		return nil, err
	}
	return &Pipeline{steps: steps}, nil
}

// Naive 构建朴素流水线：每个算子独立一步，不重排、不合并，作为语义基准。
func Naive(ops []Op) (*Pipeline, error) {
	var steps []step
	for _, o := range ops {
		if o.stateful() {
			steps = append(steps, step{state: o.State})
			continue
		}
		if err := o.Pred.Valid(); err != nil {
			return nil, err
		}
		steps = append(steps, step{p: o.Pred})
	}
	return &Pipeline{steps: steps}, nil
}

// Feed 让一批事件依次经过链上每一步。任一步判 false 即短路。
// 失败不留痕：先整批校验事件，全部合法才逐条求值；
// 任一事件非法则整批不生效，任何状态过滤器都不会被触碰。
func (p *Pipeline) Feed(evs []pred.Event) ([]pred.Event, error) {
	for _, e := range evs {
		if err := e.Valid(); err != nil {
			return nil, err
		}
	}
	var out []pred.Event
	for _, e := range evs {
		pass := true
		for _, s := range p.steps {
			ok := false
			if s.state != nil {
				ok = s.state.Eval(e)
			} else {
				ok = s.p.Eval(e)
			}
			if !ok {
				pass = false
				break
			}
		}
		if pass {
			out = append(out, e)
		}
	}
	return out, nil
}
