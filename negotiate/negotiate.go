// Package negotiate 依据客户端声明的版本区间，在注册表快照上选出
// 后端可服务的最高版本并生成适配步骤计划。协商是只读操作。
package negotiate

import (
	"fmt"

	"ontology/adapter"
	"ontology/version"
)

// Kind 区分协商失败的原因类别。
type Kind int

const (
	// KindInvalidArgument 表示参数非法（scopes 含空串）。
	KindInvalidArgument Kind = iota
	// KindSyntax 表示区间串语法错误。
	KindSyntax
	// KindInvalidTime 表示 now 不在 [0, adapter.MaxTime]。
	KindInvalidTime
	// KindFutureVersion 表示区间下界 lo 超过头版本 H。
	KindFutureVersion
	// KindNoPermission 表示候选版本是预览版且未持有其作用域。
	KindNoPermission
	// KindSunset 表示候选的适配链上有版本已下线。
	KindSunset
	// KindNoPath 表示候选的适配链上有适配步未登记（Step 为首个缺失步）。
	KindNoPath
	// KindLossy 表示严格模式下候选的适配链上有请求有损步（Step 为首个有损步）。
	KindLossy
)

// Error 是一次失败的协商。
type Error struct {
	Kind Kind
	Step int // 仅 KindNoPath / KindLossy 有效，为链上首个缺失/有损步
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Plan 是一次成功协商的适配步骤计划，取自同一个注册表快照。
type Plan struct {
	Version         int   // 选中的服务版本 v
	Steps           []int // 适配步列表 [v..H-1]，长度恒等于 H-v
	ReqLossySteps   int   // 链上请求有损步数
	RespLossySteps  int   // 链上响应有损步数（只计数不阻断）
	Warning         int64 // 链上已登记下线者的 min(t-now)，无则为 -1
	RegistryVersion uint64
}

// Negotiator 在某个注册表上执行协商，可并发使用。
type Negotiator struct {
	reg *adapter.Registry
}

// New 创建绑定到 reg 的协商器。
func New(reg *adapter.Registry) *Negotiator {
	return &Negotiator{reg: reg}
}

// Negotiate 按区间 rangeStr 选出可服务的最高版本。
//
// 拒绝按此顺序只报第一个：参数非法（scopes 含空串）、区间语法错误、
// 时间非法、未来版本（lo>H）、候选原因（top 这个候选的原因）。
func (n *Negotiator) Negotiate(rangeStr string, allowLossyReq bool, scopes []string, now int64) (Plan, error) {
	for _, s := range scopes {
		if s == "" {
			return Plan{}, &Error{Kind: KindInvalidArgument, Msg: "negotiate: scopes must not contain empty string"}
		}
	}
	r, err := version.Parse(rangeStr)
	if err != nil {
		return Plan{}, &Error{Kind: KindSyntax, Msg: "negotiate: " + err.Error()}
	}
	if now < 0 || now > adapter.MaxTime {
		return Plan{}, &Error{Kind: KindInvalidTime, Msg: fmt.Sprintf("negotiate: now %d out of range [0,%d]", now, adapter.MaxTime)}
	}

	snap := n.reg.Snapshot()
	if r.Lo > snap.H {
		return Plan{}, &Error{Kind: KindFutureVersion, Msg: fmt.Sprintf("negotiate: lo %d exceeds head version %d", r.Lo, snap.H)}
	}

	scopeSet := make(map[string]struct{}, len(scopes))
	for _, s := range scopes {
		scopeSet[s] = struct{}{}
	}

	top := r.Hi
	if top > snap.H {
		top = snap.H
	}
	var topErr *Error
	for v := top; v >= r.Lo; v-- {
		cerr := candidateErr(snap, v, allowLossyReq, scopeSet, now)
		if cerr == nil {
			return buildPlan(snap, v, now), nil
		}
		if v == top {
			topErr = cerr
		}
	}
	return Plan{}, topErr
}

// candidateErr 依次检查候选 v 的四项可服务条件，返回第一个不满足的原因。
func candidateErr(snap adapter.Snapshot, v int, allowLossyReq bool, scopes map[string]struct{}, now int64) *Error {
	if scope, ok := snap.Previews[v]; ok {
		if _, held := scopes[scope]; !held {
			return &Error{Kind: KindNoPermission, Msg: fmt.Sprintf("negotiate: version %d is preview, scope %q required", v, scope)}
		}
	}
	for u := v; u < snap.H; u++ {
		if t, ok := snap.Sunsets[u]; ok && now >= t {
			return &Error{Kind: KindSunset, Msg: fmt.Sprintf("negotiate: version %d on chain of %d sunset at %d", u, v, t)}
		}
	}
	for u := v; u < snap.H; u++ {
		if _, ok := snap.Steps[u]; !ok {
			return &Error{Kind: KindNoPath, Step: u, Msg: fmt.Sprintf("negotiate: adapter step %d missing on chain of %d", u, v)}
		}
	}
	if !allowLossyReq {
		for u := v; u < snap.H; u++ {
			if snap.Steps[u].ReqLossy {
				return &Error{Kind: KindLossy, Step: u, Msg: fmt.Sprintf("negotiate: adapter step %d on chain of %d is request-lossy", u, v)}
			}
		}
	}
	return nil
}

// buildPlan 为可服务版本 v 生成计划，与候选检查使用同一快照。
func buildPlan(snap adapter.Snapshot, v int, now int64) Plan {
	h := snap.H
	plan := Plan{
		Version:         v,
		Steps:           make([]int, 0, h-v),
		Warning:         -1,
		RegistryVersion: snap.Version,
	}
	for u := v; u < h; u++ {
		plan.Steps = append(plan.Steps, u)
		step := snap.Steps[u]
		if step.ReqLossy {
			plan.ReqLossySteps++
		}
		if step.RespLossy {
			plan.RespLossySteps++
		}
		if t, ok := snap.Sunsets[u]; ok {
			if remain := t - now; plan.Warning < 0 || remain < plan.Warning {
				plan.Warning = remain
			}
		}
	}
	return plan
}
