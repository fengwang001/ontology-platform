package lifecycle

import (
	"fmt"
	"sort"
)

// stage 是单个实例在处理单元内的暂定变更。
type stage struct {
	before  *Instance // 加锁后的真实快照（读集版本记录在其中）
	state   State     // 暂定状态
	attrs   map[AttrKey]AttrValue
	linkAdd map[LinkType]map[InstanceID]struct{}
	linkDel map[LinkType]map[InstanceID]struct{}
}

// view 是一次处理单元内的“生效后投影”：在真实 Store 之上叠加本单元
// 暂定的状态/属性/链接变更。所有前置条件、迁移后基数校验与跨实例钩子
// 都在同一个投影上求值，因此它们看到的是彼此一致的迁移后状态，不存在
// “状态已迁移而链接/属性尚未满足约束”的中间情形。
type view struct {
	store  *Store
	stages map[InstanceID]*stage
}

func newView(s *Store) *view {
	return &view{store: s, stages: map[InstanceID]*stage{}}
}

// ensureStage 为实例建立暂定层；必须在持有该实例锁后、基于真实快照调用。
func (v *view) ensureStage(snap *Instance) *stage {
	st, ok := v.stages[snap.ID]
	if !ok {
		st = &stage{
			before:  snap,
			state:   snap.State,
			attrs:   map[AttrKey]AttrValue{},
			linkAdd: map[LinkType]map[InstanceID]struct{}{},
			linkDel: map[LinkType]map[InstanceID]struct{}{},
		}
		for k, val := range snap.Attrs {
			st.attrs[k] = val
		}
		v.stages[snap.ID] = st
	}
	return st
}

// stateOf 返回投影中的当前状态（暂定状态优先）。
func (v *view) stateOf(id InstanceID) (State, string, bool) {
	if st, ok := v.stages[id]; ok {
		return st.state, st.before.Type, true
	}
	inst, ok := v.store.instances[id]
	if !ok {
		return "", "", false
	}
	return inst.State, inst.Type, true
}

// attrOf 返回投影中的属性值。
func (v *view) attrOf(id InstanceID, key AttrKey) (AttrValue, bool) {
	if st, ok := v.stages[id]; ok {
		val, exists := st.attrs[key]
		return val, exists
	}
	inst, ok := v.store.instances[id]
	if !ok {
		return nil, false
	}
	val, exists := inst.Attrs[key]
	return val, exists
}

// neighborsOf 返回投影中的出向邻居（稳定排序）。
func (v *view) neighborsOf(src InstanceID, link LinkType) []InstanceID {
	set := map[InstanceID]struct{}{}
	if m, ok := v.store.links[src]; ok {
		for dst := range m[link] {
			set[dst] = struct{}{}
		}
	}
	if st, ok := v.stages[src]; ok {
		for dst := range st.linkDel[link] {
			delete(set, dst)
		}
		for dst := range st.linkAdd[link] {
			set[dst] = struct{}{}
		}
	}
	out := make([]InstanceID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// applyAttrs 把属性变更叠加到暂定层。
func (v *view) applyAttrs(st *stage, ops []AttrOp) error {
	for _, op := range ops {
		switch op.Op {
		case AttrSet:
			st.attrs[op.Key] = op.Value
		case AttrAdd:
			n, ok := asInt64(op.Value)
			if !ok {
				return fmt.Errorf("lifecycle: AttrAdd requires int64 value, got %T", op.Value)
			}
			cur, _ := st.attrs[op.Key].(int64)
			st.attrs[op.Key] = cur + n
		default:
			return fmt.Errorf("lifecycle: unknown attr operator %q", op.Op)
		}
	}
	return nil
}

// applyLinks 把链接变更叠加到暂定层。
func (v *view) applyLinks(st *stage, ops []LinkOp) error {
	for _, op := range ops {
		switch op.Op {
		case LinkAdd:
			if st.linkAdd[op.Link] == nil {
				st.linkAdd[op.Link] = map[InstanceID]struct{}{}
			}
			delete(st.linkDel[op.Link], op.Target)
			st.linkAdd[op.Link][op.Target] = struct{}{}
		case LinkDel:
			if st.linkDel[op.Link] == nil {
				st.linkDel[op.Link] = map[InstanceID]struct{}{}
			}
			delete(st.linkAdd[op.Link], op.Target)
			st.linkDel[op.Link][op.Target] = struct{}{}
		default:
			return fmt.Errorf("lifecycle: unknown link operator %q", op.Op)
		}
	}
	return nil
}

// markState 暂定地推进实例状态。
func (v *view) markState(st *stage, to State) { st.state = to }

// checkPreconditions 在触发实例“自身迁移生效前”的投影上评估全部前置条件。
// 此时投影已包含处理单元内更早确定的暂定变更，因此同单元内各环节的
// 判定彼此一致。返回逐项结论用于日志。
func (v *view) checkPreconditions(id InstanceID, rule *TransitionRule) (bool, []string) {
	reasons := make([]string, 0, len(rule.Preconditions))
	allOK := true
	for _, pc := range rule.Preconditions {
		var ok bool
		var detail string
		switch {
		case pc.Attr != nil:
			ok, detail = v.evalAttr(id, pc.Attr)
		case pc.LinkCount != nil:
			ok, detail = v.evalLinkCount(id, pc.LinkCount)
		case pc.LinkState != nil:
			ok, detail = v.evalLinkState(id, pc.LinkState)
		default:
			ok, detail = false, "empty precondition"
		}
		reasons = append(reasons, detail)
		if !ok {
			allOK = false
		}
	}
	return allOK, reasons
}

func (v *view) evalAttr(id InstanceID, c *AttrCheck) (bool, string) {
	got, exists := v.attrOf(id, c.Key)
	if !exists {
		return false, fmt.Sprintf("attr[%s] missing, want %s %v", c.Key, c.Op, c.Value)
	}
	cmp, ok := compareValues(got, c.Value)
	if !ok {
		return false, fmt.Sprintf("attr[%s]=%v not comparable with %v", c.Key, got, c.Value)
	}
	pass := false
	switch c.Op {
	case CmpEq:
		pass = cmp == 0
	case CmpNe:
		pass = cmp != 0
	case CmpLt:
		pass = cmp < 0
	case CmpLe:
		pass = cmp <= 0
	case CmpGt:
		pass = cmp > 0
	case CmpGe:
		pass = cmp >= 0
	}
	detail := fmt.Sprintf("attr[%s]=%v %s %v -> %t", c.Key, got, c.Op, c.Value, pass)
	return pass, detail
}

func (v *view) evalLinkCount(id InstanceID, c *LinkCountCheck) (bool, string) {
	n := len(v.neighborsOf(id, c.Link))
	pass := true
	if c.Min >= 0 && n < c.Min {
		pass = false
	}
	if c.Max >= 0 && n > c.Max {
		pass = false
	}
	return pass, fmt.Sprintf("count(%s)=%d in [%d,%d] -> %t", c.Link, n, c.Min, c.Max, pass)
}

func (v *view) evalLinkState(id InstanceID, c *LinkStateCheck) (bool, string) {
	neighbors := v.neighborsOf(id, c.Link)
	if len(neighbors) == 0 {
		return false, fmt.Sprintf("link-state(%s): no neighbors, require states %v", c.Link, c.AllowedStates)
	}
	allowed := func(s State) bool {
		for _, want := range c.AllowedStates {
			if s == want {
				return true
			}
		}
		return false
	}
	if c.RequireAll {
		for _, nb := range neighbors {
			st, _, ok := v.stateOf(nb)
			if !ok || !allowed(st) {
				return false, fmt.Sprintf("link-state(%s): neighbor %s state=%s not in %v", c.Link, nb, st, c.AllowedStates)
			}
		}
		return true, fmt.Sprintf("link-state(%s): all %d neighbors in %v", c.Link, len(neighbors), c.AllowedStates)
	}
	for _, nb := range neighbors {
		if st, _, ok := v.stateOf(nb); ok && allowed(st) {
			return true, fmt.Sprintf("link-state(%s): neighbor %s state=%s matches", c.Link, nb, st)
		}
	}
	return false, fmt.Sprintf("link-state(%s): none of %d neighbors in %v", c.Link, len(neighbors), c.AllowedStates)
}

// checkPost 在“迁移生效后”的同一投影上依次评估属性校验钩子、
// 迁移后链接基数约束与跨实例状态钩子。返回固定优先级中最严重的错误
// （ErrPrecondition 先于 ErrCardinality 先于 ErrHook），全部依据
// 写入 reasons。
func (v *view) checkPost(id InstanceID, rule *TransitionRule) (*LifecycleError, []string) {
	reasons := []string{}
	var worst *LifecycleError

	consider := func(code ErrorCode, detail string, pass bool) {
		reasons = append(reasons, detail)
		if !pass && worst == nil {
			worst = newErr(code, id, rule.Name, detail)
		}
	}

	// 属性校验钩子（归类 ErrPrecondition，优先级高于基数与跨实例钩子）。
	for _, b := range rule.AttrBounds {
		val, exists := v.attrOf(id, b.Key)
		pass := true
		switch {
		case b.Min != nil || b.Max != nil:
			n, ok := asInt64(val)
			if !exists || !ok {
				pass = false
			} else {
				if b.Min != nil && n < *b.Min {
					pass = false
				}
				if b.Max != nil && n > *b.Max {
					pass = false
				}
			}
		default:
			pass = !exists
			if exists {
				pass = false
				for _, allowed := range b.AllowedValues {
					if eq, ok := compareValues(val, allowed); ok && eq == 0 {
						pass = true
						break
					}
				}
			}
		}
		consider(ErrPrecondition, fmt.Sprintf("attr-bound[%s]=%v ok=%t", b.Key, val, pass), pass)
	}

	// 迁移后链接基数：使用投影（即迁移生效后）的真实链接数量。
	for _, cb := range rule.Cardinality {
		n := len(v.neighborsOf(id, cb.Link))
		pass := n >= cb.Min && (cb.Max < 0 || n <= cb.Max)
		code := ErrCardinality
		// 属性类错误优先级更高，保留已有 worst；基数错误只在尚无更严重错误时占位。
		reasons = append(reasons, fmt.Sprintf("post-cardinality(%s)=%d in [%d,%d] ok=%t", cb.Link, n, cb.Min, cb.Max, pass))
		if !pass && (worst == nil || worst.Code > code) {
			worst = newErr(code, id, rule.Name,
				fmt.Sprintf("post cardinality of %s is %d, want [%d,%d]", cb.Link, n, cb.Min, cb.Max))
		}
	}

	// 跨实例联动钩子：邻居必须处于链接类型要求的状态。
	for _, h := range rule.Hooks {
		neighbors := v.neighborsOf(id, h.Link)
		pass := true
		bad := InstanceID("")
		badState := State("")
		for _, nb := range neighbors {
			st, _, ok := v.stateOf(nb)
			if !ok {
				pass, bad, badState = false, nb, "<missing>"
				break
			}
			matched := false
			for _, want := range h.RequireStates {
				if st == want {
					matched = true
					break
				}
			}
			if !matched {
				pass, bad, badState = false, nb, st
				break
			}
		}
		code := ErrHook
		reasons = append(reasons, fmt.Sprintf("hook(%s) require=%v ok=%t", h.Link, h.RequireStates, pass))
		if !pass && (worst == nil || worst.Code > code) {
			worst = newErr(code, id, rule.Name,
				fmt.Sprintf("hook on %s: neighbor %s in state %s not in %v", h.Link, bad, badState, h.RequireStates))
		}
	}

	return worst, reasons
}

// asInt64 接受具名 int64 类型与各 Go 整数类型的常见取值。
func asInt64(v AttrValue) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	}
	return 0, false
}

// compareValues 对数值做数值比较，其余做相等/不等比较。
// 返回 -1/0/1；不可比较时 ok=false。
func compareValues(a, b AttrValue) (int, bool) {
	if na, ok1 := asInt64(a); ok1 {
		if nb, ok2 := asInt64(b); ok2 {
			switch {
			case na < nb:
				return -1, true
			case na > nb:
				return 1, true
			default:
				return 0, true
			}
		}
	}
	if a == b {
		return 0, true
	}
	return 0, false
}
