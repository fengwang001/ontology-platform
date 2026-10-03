package negotiate

import (
	"errors"
	"fmt"

	"ontology/adapter"
	"ontology/version"
)

const maxTime int64 = 1_000_000_000_000_000

// Reason 是候选不可服务或协商失败的错误类别。
type Reason int

const (
	ReasonInvalidArgument Reason = iota + 1
	ReasonSyntax
	ReasonInvalidTime
	ReasonFuture
	ReasonNoPermission
	ReasonSunset
	ReasonNoPath
	ReasonLossy
)

// String 返回错误类别的稳定名称。
func (r Reason) String() string {
	switch r {
	case ReasonInvalidArgument:
		return "invalid-argument"
	case ReasonSyntax:
		return "syntax"
	case ReasonInvalidTime:
		return "invalid-time"
	case ReasonFuture:
		return "future"
	case ReasonNoPermission:
		return "no-permission"
	case ReasonSunset:
		return "sunset"
	case ReasonNoPath:
		return "no-path"
	case ReasonLossy:
		return "lossy"
	default:
		return "unknown"
	}
}

// Plan 是一次成功协商的适配计划。
type Plan struct {
	Version     int
	Steps       []int
	ReqLossy    int
	RespLossy   int
	Warning     int64
	RegistryRev int
}

// Error 携带可精确复现的失败类别与相关步号。
type Error struct {
	Reason Reason
	Step   int
}

func (e *Error) Error() string {
	if e.Step > 0 {
		return fmt.Sprintf("negotiate: %s (step %d)", e.Reason, e.Step)
	}
	return "negotiate: " + e.Reason.String()
}

// Negotiator 在固定头版本上执行只读协商。
type Negotiator struct {
	registry *adapter.Registry
}

// New 创建协商器。
func New(registry *adapter.Registry) *Negotiator {
	return &Negotiator{registry: registry}
}

// Negotiate 在注册表的单一快照上选出区间内最高的可服务版本并给出计划。
// 该方法是只读的，不推进注册表版本号。
func (n *Negotiator) Negotiate(rangeText string, allowLossyReq bool, scopes []string, now int64) (Plan, error) {
	for _, s := range scopes {
		if s == "" {
			return Plan{}, &Error{Reason: ReasonInvalidArgument}
		}
	}
	rng, err := version.Parse(rangeText)
	if err != nil {
		return Plan{}, &Error{Reason: ReasonSyntax}
	}
	if now < 0 || now > maxTime {
		return Plan{}, &Error{Reason: ReasonInvalidTime}
	}

	snap := n.registry.SnapshotAt()
	h := snap.H
	if rng.Lo > h {
		return Plan{}, &Error{Reason: ReasonFuture}
	}

	scopeSet := make(map[string]struct{}, len(scopes))
	for _, s := range scopes {
		scopeSet[s] = struct{}{}
	}

	top := rng.Hi
	if top > h {
		top = h
	}

	var topReason *Error
	for v := top; v >= rng.Lo; v-- {
		reason := evaluate(v, snap, allowLossyReq, scopeSet, now)
		if v == top {
			topReason = reason
		}
		if reason == nil {
			return buildPlan(v, snap, now), nil
		}
	}
	return Plan{}, topReason
}

// evaluate 按“无权限 > 已下线 > 无路径 > 有损”的次序返回首个不可服务原因。
func evaluate(v int, snap adapter.Snapshot, allowLossyReq bool, scopes map[string]struct{}, now int64) *Error {
	if want, ok := snap.Preview[v]; ok {
		if _, hold := scopes[want]; !hold {
			return &Error{Reason: ReasonNoPermission}
		}
	}
	for x := v; x < snap.H; x++ {
		if t, ok := snap.Sunset[x]; ok && now >= t {
			return &Error{Reason: ReasonSunset}
		}
	}
	for x := v; x < snap.H; x++ {
		if _, ok := snap.Steps[x]; !ok {
			return &Error{Reason: ReasonNoPath, Step: x}
		}
	}
	if !allowLossyReq {
		for x := v; x < snap.H; x++ {
			if snap.Steps[x].ReqLossy {
				return &Error{Reason: ReasonLossy, Step: x}
			}
		}
	}
	return nil
}

// buildPlan 只在 evaluate 通过后调用，统计内容同样取自同一快照。
func buildPlan(v int, snap adapter.Snapshot, now int64) Plan {
	steps := make([]int, 0, snap.H-v)
	reqLossy := 0
	respLossy := 0
	for x := v; x < snap.H; x++ {
		steps = append(steps, x)
		step := snap.Steps[x]
		if step.ReqLossy {
			reqLossy++
		}
		if step.RespLossy {
			respLossy++
		}
	}
	warning := int64(-1)
	for x := v; x < snap.H; x++ {
		if t, ok := snap.Sunset[x]; ok {
			delta := t - now
			if warning == -1 || delta < warning {
				warning = delta
			}
		}
	}
	return Plan{
		Version:     v,
		Steps:       steps,
		ReqLossy:    reqLossy,
		RespLossy:   respLossy,
		Warning:     warning,
		RegistryRev: snap.Rev,
	}
}

// ReasonOf 提取错误类别，非协商错误返回 0。
func ReasonOf(err error) Reason {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return 0
}
