// Package verdict 把年龄、新鲜期与请求/响应指令合成最终缓存判定。
package verdict

import (
	"errors"

	"ontology/age"
	"ontology/directive"
)

// Decision 是三类最终结果。
type Decision int

const (
	Hit        Decision = iota // 直接命中
	Revalidate                 // 必须再验证
	Unusable                   // 不可使用
)

// Reason 标明判定由哪一条规则决定。
type Reason int

const (
	ReasonStore          Reason = iota // 响应 no-store
	ReasonRespNoCache                  // 响应 no-cache
	ReasonReqNoCache                   // 请求 no-cache
	ReasonFresh                        // 年龄 + min-fresh 仍新鲜
	ReasonMaxStale                     // max-stale 容忍过期
	ReasonStale                        // 已过期且无容忍
	ReasonReqMaxAge                    // 请求 max-age 被超过
	ReasonMustRevalidate               // 过期且 must-revalidate
	ReasonOnlyIfCached                 // only-if-cached 下无法再验证
)

// ErrClockNotInjected 表示调用方未提供注入时钟。
var ErrClockNotInjected = errors.New("clock not injected")

// Input 是一次判定的全部输入。
type Input struct {
	Entry   age.Entry
	Request directive.Set
	Clock   age.Clock
}

// Result 携带判定、理由与用于观测的中间量。
type Result struct {
	Decision  Decision
	Reason    Reason
	Age       int64
	Freshness int64
	Source    string
}

// Decide 合成最终判定。
func Decide(in Input) (Result, error) {
	if in.Clock == nil {
		return Result{}, ErrClockNotInjected
	}
	now := in.Clock.Now()
	curAge, err := age.CurrentAge(in.Entry, now)
	if err != nil {
		return Result{}, err
	}
	fresh, source, err := age.Freshness(in.Entry)
	if err != nil {
		return Result{}, err
	}
	res := Result{Age: curAge, Freshness: fresh, Source: source}
	resp := in.Entry.Response
	req := in.Request

	switch {
	case resp.Has("no-store"):
		res.Decision, res.Reason = Unusable, ReasonStore
		return res, nil
	case resp.Has("no-cache"):
		res.Decision, res.Reason = Revalidate, ReasonRespNoCache
	case req.Has("no-cache"):
		res.Decision, res.Reason = Revalidate, ReasonReqNoCache
	default:
		decideFreshness(&res, curAge, fresh, req, resp)
	}

	// 请求 max-age=N 且 age>N：覆盖「直接命中」。
	if d, ok := req.Get("max-age"); ok && d.HasDelta && curAge > d.Delta && res.Decision == Hit {
		res.Decision, res.Reason = Revalidate, ReasonReqMaxAge
	}
	// only-if-cached：本该再验证 ⇒ 不可使用。
	if req.Has("only-if-cached") && res.Decision == Revalidate {
		res.Decision, res.Reason = Unusable, ReasonOnlyIfCached
	}
	return res, nil
}

func decideFreshness(res *Result, curAge, fresh int64, req, resp directive.Set) {
	minFresh := int64(0)
	if d, ok := req.Get("min-fresh"); ok && d.HasDelta {
		minFresh = d.Delta
	}
	if curAge+minFresh < fresh {
		res.Decision, res.Reason = Hit, ReasonFresh
		return
	}
	if d, ok := req.Get("max-stale"); ok {
		if resp.Has("must-revalidate") {
			res.Decision, res.Reason = Revalidate, ReasonMustRevalidate
			return
		}
		if d.Infinite || (d.HasDelta && curAge-fresh <= d.Delta) {
			res.Decision, res.Reason = Hit, ReasonMaxStale
			return
		}
	}
	res.Decision, res.Reason = Revalidate, ReasonStale
}
