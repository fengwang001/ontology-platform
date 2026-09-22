package vote

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrUnknownVoter 表示向未注册的参与者 ID 收集投票。
var ErrUnknownVoter = errors.New("vote: unknown voter")

// Tally 收集一次事务的投票并给出裁决。决议一旦写下不可更改。
// 所有方法都可被多 goroutine 并发调用。
type Tally struct {
	mu         sync.Mutex
	order      []string // 注册顺序，保证超时责任者确定
	registered map[string]bool
	voted      map[string]bool
	approved   map[string]bool
	deadline   time.Time
	outcome    Outcome
	decided    bool
}

// NewTally 为一组参与者创建计票器。ids 中重复即报错。
// deadline 采用左闭右开语义：now 恰好等于 deadline 即算超时。
func NewTally(ids []string, deadline time.Time) (*Tally, error) {
	t := &Tally{
		registered: make(map[string]bool, len(ids)),
		voted:      make(map[string]bool, len(ids)),
		approved:   make(map[string]bool, len(ids)),
		deadline:   deadline,
	}
	for _, id := range ids {
		if t.registered[id] {
			return nil, fmt.Errorf("vote: duplicate voter %q", id)
		}
		t.registered[id] = true
		t.order = append(t.order, id)
	}
	return t, nil
}

// Cast 记录一票。yes 为同意，否则为否决。
// 决议写下后迟到的票被忽略（返回 nil），不会改写决议。
func (t *Tally) Cast(id string, yes bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.registered[id] {
		return ErrUnknownVoter
	}
	if t.decided {
		return nil
	}
	t.voted[id] = true
	t.approved[id] = yes
	return nil
}

// Decide 按注入的 now 裁决。未满足任何终态条件时返回零值 Outcome。
// 空参与者集合视为「全票同意」（vacuous truth），裁决为 Commit。
func (t *Tally) Decide(now time.Time) Outcome {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.decided {
		return t.outcome
	}
	if o, ok := t.adjudicate(now); ok {
		t.outcome = o
		t.decided = true
	}
	return t.outcome
}

// adjudicate 计算裁决；第二个返回值表示是否已达终态。
func (t *Tally) adjudicate(now time.Time) (Outcome, bool) {
	for _, id := range t.order {
		if t.voted[id] && !t.approved[id] {
			return Outcome{Decision: Abort, Reason: ReasonRejected, Culprit: id}, true
		}
	}
	missing := ""
	for _, id := range t.order {
		if !t.voted[id] {
			missing = id
			break
		}
	}
	if missing == "" {
		return Outcome{Decision: Commit, Reason: ReasonNone}, true
	}
	// 左闭右开：now == deadline 即超时，未回复者按否决处理。
	if !now.Before(t.deadline) {
		return Outcome{Decision: Abort, Reason: ReasonTimeout, Culprit: missing}, true
	}
	return Outcome{}, false
}
