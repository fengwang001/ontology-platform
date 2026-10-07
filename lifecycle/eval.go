package lifecycle

import "fmt"

// evalPreconds 在迁移生效前的视图上求值全部前置条件。
// 返回逐条判定依据与第一个导致失败的错误（若有）。
func (e *Engine) evalPreconds(wv *workView, inst *Instance, t *Transition) ([]string, *Error) {
	var basis []string
	for _, p := range t.Preconds {
		ok, line := e.evalOnePrecond(wv, inst, p)
		basis = append(basis, line)
		if !ok {
			return basis, &Error{
				Code:       CodePrecondition,
				InstanceID: inst.ID,
				Transition: t.Name,
				Detail:     fmt.Sprintf("前置条件不成立: %s", p.Desc),
			}
		}
	}
	return basis, nil
}

func (e *Engine) evalOnePrecond(wv *workView, inst *Instance, p Precondition) (bool, string) {
	switch p.Kind {
	case PrecondAttr:
		got, exists := wv.attr(inst.ID, p.Attr)
		ok := exists && equalValue(got, p.Equals)
		return ok, fmt.Sprintf("precond attr %s: got=%v want=%v -> %v",
			p.Attr, got, p.Equals, ok)
	case PrecondLinkCount:
		n := wv.linkCount(inst.ID, p.LinkType)
		ok := (p.MinCount < 0 || n >= p.MinCount) && (p.MaxCount < 0 || n <= p.MaxCount)
		return ok, fmt.Sprintf("precond count(%s): got=%d range=[%d,%d] -> %v",
			p.LinkType, n, p.MinCount, p.MaxCount, ok)
	case PrecondPeerState:
		peers := wv.peers(inst.ID, p.LinkType)
		for _, pid := range peers {
			st, _ := wv.stateOf(pid)
			if !containsState(p.States, st) {
				return false, fmt.Sprintf("precond peers(%s): peer=%s state=%s not in %v -> false",
					p.LinkType, pid, st, p.States)
			}
		}
		return true, fmt.Sprintf("precond peers(%s): %d peers all in %v -> true",
			p.LinkType, len(peers), p.States)
	default:
		return false, "未知前置条件种类"
	}
}

// evalPost 在迁移生效后的视图上执行基数与钩子校验。
// 基数失败（CodeCardinality）优先于钩子失败（CodeHook）报告。
func (e *Engine) evalPost(wv *workView, inst *Instance, t *Transition) ([]string, *Error) {
	var basis []string
	var cardErr, hookErr *Error
	for _, r := range t.MaxCard {
		n := wv.linkCount(inst.ID, r.LinkType)
		ok := n <= r.Max
		basis = append(basis, fmt.Sprintf("post cardinality %s: count=%d max=%d -> %v",
			r.LinkType, n, r.Max, ok))
		if !ok && cardErr == nil {
			cardErr = &Error{
				Code:       CodeCardinality,
				InstanceID: inst.ID,
				Transition: t.Name,
				Detail: fmt.Sprintf("迁移后链接 %s 基数 %d 超过上限 %d",
					r.LinkType, n, r.Max),
			}
		}
	}
	for _, h := range t.Hooks {
		peers := wv.peers(inst.ID, h.LinkType)
		for _, pid := range peers {
			st, _ := wv.stateOf(pid)
			ok := containsState(h.States, st)
			basis = append(basis, fmt.Sprintf("post hook %s: peer=%s state=%s want in %v -> %v",
				h.LinkType, pid, st, h.States, ok))
			if !ok && hookErr == nil {
				hookErr = &Error{
					Code:       CodeHook,
					InstanceID: inst.ID,
					Transition: t.Name,
					Detail: fmt.Sprintf("跨实例钩子拒绝: %s 经 %s 连接的 %s 处于状态 %s，要求 %v",
						inst.ID, h.LinkType, pid, st, h.States),
				}
			}
		}
	}
	if cardErr != nil {
		return basis, cardErr
	}
	return basis, hookErr
}
