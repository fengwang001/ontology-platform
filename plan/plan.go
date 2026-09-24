// Package plan 负责批次预演：按批内顺序维护累积状态，
// 找出第一条失败事件；全部通过时产出待应用的变更集。依赖 ev。
package plan

import (
	"fmt"

	"ontology/ev"
)

// View 是对可见视图的只读查找：返回键的值与存在性。
type View func(key string) (val string, ok bool)

// Change 是一条待应用的变更（Expect 已校验通过，应用时不再需要）。
type Change struct {
	Del bool
	Key string
	Val string
}

// Rehearser 预演批次。lookups 记录最近一次 Rehearse
// 对照可见视图做过的键查找次数（非导出，不进公开接口）。
type Rehearser struct {
	lookups int
}

// Rehearse 按批内顺序逐条检查事件的前置期望。
// 每条事件的判定基于 view 叠加本批前序事件效果后的累积状态。
// 全部通过时返回待应用变更集；遇到第一条失败事件即停止并返回
// 携带其序号的错误，视图不做任何修改。
func (r *Rehearser) Rehearse(evs []ev.Event, view View) ([]Change, error) {
	r.lookups = 0
	overlay := map[string]*string{} // nil 值表示批内已删除
	changes := make([]Change, 0, len(evs))
	for i, e := range evs {
		if e.Key == "" {
			return nil, fmt.Errorf("event %d: %w", i, ev.ErrEmptyKey)
		}
		val, ok := overlay[e.Key]
		cur, curOK := "", false
		if ok {
			cur, curOK = deref(val)
		} else {
			r.lookups++
			cur, curOK = view(e.Key)
		}
		if err := ev.CheckExpect(e, cur, curOK); err != nil {
			return nil, fmt.Errorf("event %d: %w", i, err)
		}
		switch e.Kind {
		case ev.PutKind:
			v := e.Val
			overlay[e.Key] = &v
			changes = append(changes, Change{Key: e.Key, Val: e.Val})
		case ev.DelKind:
			overlay[e.Key] = nil
			changes = append(changes, Change{Del: true, Key: e.Key})
		}
	}
	return changes, nil
}

func deref(s *string) (string, bool) {
	if s == nil {
		return "", false
	}
	return *s, true
}
