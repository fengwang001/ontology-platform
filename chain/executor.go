package chain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CanonicalInput 将一组输入参数规范化为稳定字符串，作为自我触发判定依据。
// 键按字典序排列；值采用 %#v 归一，相同语义输入得到相同键。
func CanonicalInput(p Params) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		fmt.Fprintf(&b, "%#v", p[k])
		b.WriteByte(';')
	}
	return b.String()
}

type selfTriggerError struct{ frame FrameKey }

func (e *selfTriggerError) Error() string {
	return fmt.Sprintf("self-trigger detected: action %q with input %s already active in this chain",
		e.frame.Action, e.frame.Input)
}

// declarationError 表示链条声明期就必须拦截的错误。
type declarationError struct{ msg string }

func (e *declarationError) Error() string { return e.msg }

// executor 是单次顶层链条执行的内部状态机：
// 维护调用栈（自我触发检测）、各帧写入计划、输出与审计帧记录。
type executor struct {
	reg    *Registry
	frames []*FrameRecord
	stack  []FrameKey // 当前仍在执行中的帧键（长度即当前嵌套深度）
}

// run 在给定进入视图上执行 entry 动作并返回顶层帧记录。
// 它只负责“在某个状态快照上纯函数地计算”，不触碰持久化存储；
// 提交、重放与并发由 Engine 负责。
func (x *executor) run(entry string, input Params, view *View) *FrameRecord {
	return x.runFrame(entry, input, view)
}

// frameCtx 实现 ExecContext。
type frameCtx struct {
	input     Params
	view      *View
	childOut  map[string]map[string]any // childName -> 该子调用输出 map
	effectOut map[string]any
}

func (c *frameCtx) Input() Params { return c.input }
func (c *frameCtx) State() *View  { return c.view }

// Output 暴露此前已完成关键子调用的输出。
// 子调用只有一个输出键时直接返回该值，多个键时整体返回输出 map。
func (c *frameCtx) Output(name string) (any, bool) {
	out, ok := c.childOut[name]
	if !ok {
		return nil, false
	}
	if len(out) == 1 {
		for _, v := range out {
			return v, true
		}
	}
	return out, true
}

// childOutputLookup 返回 Bind 所需的输出查询闭包。
func (c *frameCtx) childOutputLookup() func(name string) (any, bool) {
	return c.Output
}

// runFrame 执行一个动作帧及其全部已声明子调用。
// view 是进入该帧时的状态视图（持久化 + 所有外层已计算的写入计划）。
func (x *executor) runFrame(name string, input Params, view *View) *FrameRecord {
	action, _ := x.reg.Get(name)
	rec := &FrameRecord{
		Depth:     len(x.stack),
		Action:    name,
		Input:     input,
		Key:       FrameKey{Action: name, Input: CanonicalInput(input)},
		StartedAt: time.Now(),
	}

	// 1) 自我触发检测：优先于前置条件评估；只扫描当前链条自身调用栈，
	// 比较次数不超过当前嵌套深度，与历史链条总数无关。
	for _, k := range x.stack {
		if k == rec.Key {
			rec.Status = StatusSelfTriggerRejected
			rec.Err = (&selfTriggerError{frame: rec.Key}).Error()
			x.frames = append(x.frames, rec)
			return rec
		}
	}
	x.stack = append(x.stack, rec.Key)
	defer func() { x.stack = x.stack[:len(x.stack)-1] }()
	x.frames = append(x.frames, rec)

	ctx := &frameCtx{input: input, view: view, childOut: map[string]map[string]any{}}

	// 2) 前置条件独立重新评估：看到进入视图（含外层中间写入计划），
	// 但看不到本帧此后才计算出的写入。
	pass, basis := action.Pre(ctx)
	rec.PreBasis = basis
	if !pass {
		rec.Status = StatusPreFailed
		rec.Err = fmt.Sprintf("precondition failed: %s", basis)
		return rec
	}

	// 3) 按声明顺序触发子调用。plan 累积本帧进入后可见的全部写入计划。
	var plan []WriteOp
	aborted := false
	for _, ch := range action.Children {
		if ch.When != nil && !ch.When(input, ctx.childOutputLookup()) {
			continue
		}
		childInput := ch.Bind(input, ctx.childOutputLookup())
		// 内层看到外层截至触发前已经计算出的写入计划（即便尚未提交）。
		childView := view.Push(plan)
		childRec := x.runFrame(ch.Name, childInput, childView)

		failed := childRec.Status != StatusCompleted
		if failed && !ch.Critical {
			// 非关键调用的任何失败都只记录、不放弃：其写入计划被丢弃，
			// 名字不进入 childOut，后续无法把它当作成功。
			childRec.Status = StatusNonCriticalFailed
			continue
		}
		if failed {
			// 关键调用失败：外层此前计划 + 内层计划整体放弃（plan 直接丢弃）。
			plan = nil
			aborted = true
			break
		}
		plan = append(plan, childRec.WritePlan...)
		ctx.childOut[ch.Name] = childRec.Outputs
	}
	if aborted {
		rec.WritePlan = nil
		rec.Status = StatusAborted
		rec.Err = "aborted due to failed critical nested call"
		return rec
	}

	// 4) 本帧效果：此时视图包含此前已完成关键子调用的写入计划。
	ctx.view = view.Push(plan)
	ops, outputs, effectBasis, err := action.Effect(ctx)
	rec.EffectBasis = effectBasis
	if err != nil {
		rec.WritePlan = append(plan, ops...) // 已计算部分留痕，但随整体放弃
		rec.Status = StatusPostFailed
		rec.Err = fmt.Sprintf("effect failed: %v", err)
		return rec
	}
	plan = append(plan, ops...)

	// 5) 后置条件：在叠加本帧全部写入计划后的视图上评估。
	ctx.view = view.Push(plan)
	ctx.effectOut = outputs
	postOK, postBasis := action.Post(ctx, outputs)
	rec.PostBasis = postBasis
	if !postOK {
		rec.WritePlan = plan
		rec.Status = StatusPostFailed
		rec.Err = fmt.Sprintf("postcondition failed: %s", postBasis)
		return rec
	}

	rec.WritePlan = plan
	rec.Outputs = outputs
	rec.Status = StatusCompleted
	return rec
}
