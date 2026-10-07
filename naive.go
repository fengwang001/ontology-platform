package ontology

import "fmt"

// 本文件是独立实现的朴素逐事件重放模型，用作差异测试的对照基准。
// 它刻意不使用快照、不共享 replay.go 的重放逻辑，仅以最直接的方式
// 从头逐条解释事件，从而与优化路径形成真正独立的两份实现。

// naiveVersionAt 线性扫描版本列表（刻意不用二分）。
func naiveVersionAt(versions []RuleVersion, t int64) *RuleVersion {
	var best *RuleVersion
	for i := range versions {
		if versions[i].ValidFrom <= t {
			best = &versions[i]
		}
	}
	return best
}

// naiveEffectiveRange 沿父链逐级向上查找最近声明（独立实现）。
func naiveEffectiveRange(rv *RuleVersion, typeID, prop string) (Range, bool) {
	cur := typeID
	for cur != "" {
		t, ok := rv.Types[cur]
		if !ok {
			return nil, false
		}
		if r, ok := t.Props[prop]; ok {
			return r, true
		}
		cur = t.Parent
	}
	return nil, false
}

// naiveRelated 判断 a 与 b 是否位于同一条继承链上（互为祖先/后代）。
func naiveRelated(rv *RuleVersion, a, b string) bool {
	ancestorsOf := func(x string) map[string]bool {
		out := map[string]bool{}
		for cur := x; cur != ""; {
			t, ok := rv.Types[cur]
			if !ok {
				break
			}
			cur = t.Parent
			if cur != "" {
				out[cur] = true
			}
		}
		return out
	}
	return ancestorsOf(a)[b] || ancestorsOf(b)[a]
}

// NaiveReplay 从头逐事件重放，返回截止时刻 cutoff（含）的状态。
// 错误语义与 Store.Rebuild 完全一致。
func NaiveReplay(events []Event, cutoff int64, rules *RuleStore) (State, error) {
	// 优先级 1：并列记录。
	for i := 1; i < len(events); i++ {
		if events[i].Time <= events[i-1].Time && events[i].Time <= cutoff {
			return State{}, fmt.Errorf("naive: 事件 %d 顺序不明: %w", i, ErrAmbiguousOrder)
		}
	}
	// 优先级 2：截止时刻早于首个事件。
	if len(events) == 0 || cutoff < events[0].Time {
		return State{}, fmt.Errorf("naive: %w", ErrCutoffBeforeFirstEvent)
	}
	versions := rules.Versions()
	typ := ""
	props := map[string]Value{}
	for _, ev := range events {
		if ev.Time > cutoff {
			continue
		}
		rv := naiveVersionAt(versions, ev.Time)
		switch ev.Kind {
		case EvCreated:
			if rv == nil {
				return State{}, fmt.Errorf("naive: %w", ErrUndefinedTargetType)
			}
			if _, ok := rv.Types[ev.TypeID]; !ok {
				return State{}, fmt.Errorf("naive: 类型 %q 未定义: %w", ev.TypeID, ErrUndefinedTargetType)
			}
			typ = ev.TypeID
			props = map[string]Value{}
		case EvPropertySet:
			if typ == "" || rv == nil {
				continue
			}
			if r, ok := naiveEffectiveRange(rv, typ, ev.Prop); ok && r.Contains(ev.Val) {
				props[ev.Prop] = ev.Val
			}
		case EvTypeEvolve:
			if rv == nil {
				return State{}, fmt.Errorf("naive: %w", ErrUndefinedTargetType)
			}
			if _, ok := rv.Types[ev.TypeID]; !ok {
				return State{}, fmt.Errorf("naive: 类型 %q 未定义: %w", ev.TypeID, ErrUndefinedTargetType)
			}
			if ev.TypeID == typ {
				continue
			}
			if typ == "" || !naiveRelated(rv, typ, ev.TypeID) {
				return State{}, fmt.Errorf("naive: %q -> %q: %w", typ, ev.TypeID, ErrInvalidEvolutionPath)
			}
			typ = ev.TypeID
			for p, v := range props {
				if r, ok := naiveEffectiveRange(rv, typ, p); !ok || !r.Contains(v) {
					delete(props, p)
				}
			}
		}
	}
	return State{TypeID: typ, Props: props}, nil
}
