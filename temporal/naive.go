package temporal

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// NaiveEngine 是独立维护的朴素参照实现：
// 每次查询都从提交/撤回事件日志出发，线性重建成当前变更集合，
// 再全量筛选、全量重放，刻意不使用任何索引或桶结构。
// 差分测试以其结果作为正确性基准。
type NaiveEngine struct {
	mu      sync.Mutex
	horizon Tick
	events  []record
	counter uint64
}

type record struct {
	change Change
	at     Tick
	kind   uint8 // 0=submit, 1=withdraw
}

func NewNaiveEngine(horizon Tick) *NaiveEngine {
	return &NaiveEngine{horizon: horizon}
}

const (
	evSubmit = iota
	evWithdraw
)

func (n *NaiveEngine) Submit(ctx context.Context, c Change) (*SubmitResult, error) {
	if c.Effective < c.Committed {
		return nil, &DomainError{Code: ErrCodeEffectiveBeforeCommit,
			Msg: "effective tick is earlier than commit tick"}
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	if c.ID == "" {
		n.counter++
		c.ID = fmt.Sprintf("n-%016d", n.counter)
	}
	current := n.materializeLocked()
	if _, exists := current[c.ID]; exists {
		return nil, &DomainError{Code: ErrCodeGeneric, Msg: "duplicate change id"}
	}
	if c.Kind != Grant && c.Kind != Deny && c.Kind != Revoke {
		return nil, &DomainError{Code: ErrCodeGeneric, Msg: "unknown kind"}
	}
	if c.Kind == Revoke {
		t, ok := current[c.Target]
		if !ok {
			return nil, &DomainError{Code: ErrCodeGeneric, Msg: "unknown revoke target"}
		}
		if t.Kind == Revoke || t.Subject != c.Subject || t.Label != c.Label {
			return nil, &DomainError{Code: ErrCodeGeneric, Msg: "bad revoke target"}
		}
		if t.Effective > c.Committed {
			return nil, &DomainError{Code: ErrCodeGeneric,
				Msg: "revoke target not yet effective"}
		}
		if c.DependsOn != "" {
			if _, ok := current[c.DependsOn]; !ok {
				return nil, &DomainError{Code: ErrCodeGeneric,
					Msg: "unknown dependency"}
			}
		}
	}
	n.events = append(n.events, record{change: c, at: c.Committed, kind: evSubmit})
	return &SubmitResult{Change: c}, nil
}

func (n *NaiveEngine) Withdraw(ctx context.Context, id string, at Tick) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	current := n.materializeLocked()
	c, ok := current[id]
	if !ok {
		return &DomainError{Code: ErrCodeGeneric, Msg: "unknown queued change"}
	}
	if c.Effective <= at {
		return &DomainError{Code: ErrCodeWithdrawAlreadyEffective,
			Msg: "change already effective"}
	}
	n.events = append(n.events, record{change: c, at: at, kind: evWithdraw})
	return nil
}

func (n *NaiveEngine) Decide(ctx context.Context, subject Subject, label Label, at Tick) (*Decision, error) {
	if at < n.horizon {
		return nil, &DomainError{Code: ErrCodeQueryBeforeHorizon,
			Msg: "query before horizon"}
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	// 朴素重建：在 [horizon, at] 区间内，撤回只影响其操作时刻仍排队的变更；
	// 历史重放按每个操作自身的时刻独立判断（撤回不改变更早的历史）。
	live := map[string]Change{}
	for _, ev := range n.events {
		if ev.kind == evSubmit {
			c := ev.change
			if c.Committed <= at {
				live[c.ID] = c
			}
			continue
		}
		if ev.at <= at {
			if c, ok := live[ev.change.ID]; ok && c.Effective > ev.at {
				delete(live, ev.change.ID)
			}
		}
	}

	var seq []Change
	for _, c := range live {
		if c.Subject == subject && c.Label == label && c.Effective <= at {
			seq = append(seq, c)
		}
	}
	sort.Slice(seq, func(i, j int) bool {
		return orderKeyOf(seq[i]).less(orderKeyOf(seq[j]))
	})
	res := replay(seq)
	return &Decision{
		At:       at,
		Subject:  subject,
		Label:    label,
		Allowed:  res.hasRule && res.allowed,
		Default:  !res.hasRule,
		Basis:    res.basis,
		Examined: len(seq),
	}, nil
}

// materializeLocked 线性重放全部事件得到“当前”变更集合（不含已撤回）。
func (n *NaiveEngine) materializeLocked() map[string]Change {
	live := map[string]Change{}
	// 撤回是否成功，取决于该撤回事件时刻被撤回变更是否仍排队。
	// 事件已按产生顺序追加；为保持朴素，这里再按事件时刻稳定排序一次。
	events := make([]record, len(n.events))
	copy(events, n.events)
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		return events[i].change.ID < events[j].change.ID
	})
	for _, ev := range events {
		switch ev.kind {
		case evSubmit:
			live[ev.change.ID] = ev.change
		case evWithdraw:
			if c, ok := live[ev.change.ID]; ok && c.Effective > ev.at {
				delete(live, ev.change.ID)
			}
		}
	}
	return live
}
