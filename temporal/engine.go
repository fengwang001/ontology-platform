package temporal

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Engine 是双轴时序权限引擎：提交时刻与生效时刻相互独立，
// 所有公开方法都可被并发调用，外部观察等价于某种串行交错顺序。
type Engine struct {
	mu      sync.RWMutex
	horizon Tick
	changes map[string]Change
	index   map[Subject]map[Label][]Change
	audit   *AuditLog
	nextID  uint64
}

// NewEngine 创建引擎。horizon 是系统已知的最早记录时刻，
// 早于该时刻的查询一律以 ErrCodeQueryBeforeHorizon 拒绝。
func NewEngine(horizon Tick, audit *AuditLog) *Engine {
	return &Engine{
		horizon: horizon,
		changes: map[string]Change{},
		index:   map[Subject]map[Label][]Change{},
		audit:   audit,
	}
}

func (e *Engine) err(code Code, format string, args ...interface{}) error {
	return &DomainError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Submit 提交一条权限规则变更。被拒绝（返回错误）的提交不改变任何排队状态。
// 对一条已生效变更的追溯撤销，返回值携带受影响后续变更在新基线下的确定结论。
func (e *Engine) Submit(ctx context.Context, c Change) (*SubmitResult, error) {
	// 第一优先级错误：生效时刻早于提交时刻。
	if c.Effective < c.Committed {
		e.auditCall("submit", c, nil, &DomainError{Code: ErrCodeEffectiveBeforeCommit,
			Msg: "effective tick is earlier than commit tick"}, nil)
		return nil, e.err(ErrCodeEffectiveBeforeCommit,
			"change %q effective %d < committed %d", c.ID, c.Effective, c.Committed)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if c.ID == "" {
		e.nextID++
		c.ID = fmt.Sprintf("c-%016d", e.nextID)
	}
	if _, dup := e.changes[c.ID]; dup {
		err := e.err(ErrCodeGeneric, "duplicate change id %q", c.ID)
		e.auditCall("submit", c, nil, err, nil)
		return nil, err
	}
	if c.Kind != Grant && c.Kind != Deny && c.Kind != Revoke {
		err := e.err(ErrCodeGeneric, "change %q has unknown kind", c.ID)
		e.auditCall("submit", c, nil, err, nil)
		return nil, err
	}

	// 结构性校验（归入通用拒绝，排在三类固定错误之后）。
	if c.Kind == Revoke {
		if err := e.validateRevoke(c); err != nil {
			e.auditCall("submit", c, nil, err, nil)
			return nil, err
		}
	}

	var reassessment []Reassessment
	if c.Kind == Revoke {
		reassessment = e.reassess(c)
	}

	e.changes[c.ID] = c
	bucket := e.bucketFor(c.Subject, c.Label)
	pos := sort.Search(len(bucket), func(i int) bool {
		return !orderKeyOf(bucket[i]).less(orderKeyOf(c))
	})
	bucket = append(bucket, Change{})
	copy(bucket[pos+1:], bucket[pos:])
	bucket[pos] = c
	e.index[c.Subject][c.Label] = bucket

	res := &SubmitResult{Change: c, Reassessment: reassessment}
	e.auditCall("submit", c, res, nil, nil)
	return res, nil
}

func (e *Engine) validateRevoke(c Change) error {
	if c.Target == "" {
		return e.err(ErrCodeGeneric, "revoke %q missing target", c.ID)
	}
	t, ok := e.changes[c.Target]
	if !ok {
		return e.err(ErrCodeGeneric, "revoke %q targets unknown change %q", c.ID, c.Target)
	}
	if t.Kind == Revoke {
		return e.err(ErrCodeGeneric, "revoke %q targets another revoke %q", c.ID, c.Target)
	}
	if t.Subject != c.Subject || t.Label != c.Label {
		return e.err(ErrCodeGeneric, "revoke %q target scope mismatch", c.ID)
	}
	// 只允许撤销在提交时刻已经生效的变更；尚在排队的应走 Withdraw。
	if t.Effective > c.Committed {
		return e.err(ErrCodeGeneric,
			"revoke %q target %q is not yet effective at commit tick %d",
			c.ID, c.Target, c.Committed)
	}
	if c.DependsOn != "" {
		if _, ok := e.changes[c.DependsOn]; !ok {
			return e.err(ErrCodeGeneric, "revoke %q depends on unknown change %q",
				c.ID, c.DependsOn)
		}
	}
	return nil
}

// reassess 重新考察所有生效时刻晚于被撤销目标、内容依赖（直接或传递）
// 于该目标所确立状态的后续变更，给出新基线下的确定结论。
func (e *Engine) reassess(rev Change) []Reassessment {
	bucket := e.index[rev.Subject][rev.Label]
	out := []Reassessment{}
	for _, d := range bucket {
		if d.ID == rev.ID || d.Effective <= e.changes[rev.Target].Effective {
			continue
		}
		if !dependsOn(bucket, d.ID, rev.Target) {
			continue
		}
		at := rev.Effective
		if d.Effective > at {
			at = d.Effective
		}
		visible := visibleAt(bucket, at)
		oldRes := replay(visible)
		newList := append(append([]Change{}, visible...), rev)
		sort.Slice(newList, func(i, j int) bool {
			return orderKeyOf(newList[i]).less(orderKeyOf(newList[j]))
		})
		newRes := replay(newList)
		oldAlive := oldRes.alive[d.ID]
		newAlive := newRes.alive[d.ID]
		reason := "still valid under the new baseline"
		if !newAlive {
			if oldAlive {
				reason = "invalidated: its prerequisite state was established by the revoked change"
			} else {
				reason = "already invalid before the revoke and remains invalid"
			}
		}
		out = append(out, Reassessment{
			ChangeID:      d.ID,
			StillValid:    newAlive,
			EffectChanged: oldAlive != newAlive,
			Reason:        reason,
		})
	}
	return out
}

// dependsOn 判断 id 是否（沿 DependsOn 链）依赖于 anc。
func dependsOn(all []Change, id, anc string) bool {
	byID := map[string]Change{}
	for _, c := range all {
		byID[c.ID] = c
	}
	seen := map[string]bool{}
	cur := id
	for cur != "" && !seen[cur] {
		if cur == anc {
			return true
		}
		seen[cur] = true
		cur = byID[cur].DependsOn
	}
	return false
}

// visibleAt 取出截至 at 时刻在提交轴与生效轴上都已可见的变更（保持全序）。
func visibleAt(bucket []Change, at Tick) []Change {
	out := make([]Change, 0, len(bucket))
	for _, c := range bucket {
		if c.Committed <= at && c.Effective <= at {
			out = append(out, c)
		}
	}
	return out
}

func (e *Engine) bucketFor(s Subject, l Label) []Change {
	labels, ok := e.index[s]
	if !ok {
		labels = map[Label][]Change{}
		e.index[s] = labels
	}
	return labels[l]
}

// Withdraw 撤回一条尚未到达生效时刻的排队变更；撤回后其对任何时刻都不再有影响。
func (e *Engine) Withdraw(ctx context.Context, id string, at Tick) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.changes[id]
	if !ok {
		err := e.err(ErrCodeGeneric, "unknown queued change %q", id)
		e.auditCall("withdraw", map[string]interface{}{"id": id, "at": at}, nil, err, nil)
		return err
	}
	// 固定错误优先级中的第三类。
	if c.Effective <= at {
		err := e.err(ErrCodeWithdrawAlreadyEffective,
			"change %q became effective at %d (withdraw at %d)", id, c.Effective, at)
		e.auditCall("withdraw", map[string]interface{}{"id": id, "at": at}, nil, err, nil)
		return err
	}

	bucket := e.index[c.Subject][c.Label]
	pos := sort.Search(len(bucket), func(i int) bool {
		return !orderKeyOf(bucket[i]).less(orderKeyOf(c))
	})
	if pos < len(bucket) && bucket[pos].ID == id {
		bucket = append(bucket[:pos], bucket[pos+1:]...)
		e.index[c.Subject][c.Label] = bucket
	}
	delete(e.changes, id)
	e.auditCall("withdraw", map[string]interface{}{"id": id, "at": at},
		map[string]string{"withdrawn": id}, nil, nil)
	return nil
}

// Decide 返回 (主体, 标签) 在 at 时刻应当生效的访问判定。
// 只读路径只接触该主体该标签的索引桶，与系统中累计的变更总数无关。
func (e *Engine) Decide(ctx context.Context, subject Subject, label Label, at Tick) (*Decision, error) {
	// 第二优先级错误：查询时刻早于已知最早记录时刻。
	if at < e.horizonSnapshot() {
		err := &DomainError{Code: ErrCodeQueryBeforeHorizon,
			Msg: fmt.Sprintf("query tick %d precedes horizon %d", at, e.horizonSnapshot())}
		e.auditCall("decide", map[string]interface{}{
			"subject": subject, "label": label, "at": at}, nil, err, nil)
		return nil, err
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	var bucket []Change
	if labels, ok := e.index[subject]; ok {
		bucket = labels[label]
	}
	eligible := visibleAt(bucket, at)
	res := replay(eligible)
	d := &Decision{
		At:       at,
		Subject:  subject,
		Label:    label,
		Allowed:  res.hasRule && res.allowed,
		Default:  !res.hasRule,
		Basis:    res.basis,
		Examined: len(eligible),
	}
	e.auditCall("decide", map[string]interface{}{
		"subject": subject, "label": label, "at": at}, d, nil, d.Basis)
	return d, nil
}

func (e *Engine) horizonSnapshot() Tick {
	e.mu.RLock()
	h := e.horizon
	e.mu.RUnlock()
	return h
}

// Snapshot 返回当前引擎内全部未撤回变更（按叠加顺序），供可观测证明使用。
func (e *Engine) Snapshot() []Change {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []Change
	for _, labels := range e.index {
		for _, bucket := range labels {
			out = append(out, bucket...)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return orderKeyOf(out[i]).less(orderKeyOf(out[j]))
	})
	return out
}

func (e *Engine) auditCall(op string, in, out interface{}, err error, basis []string) {
	if e.audit == nil {
		return
	}
	entry := AuditEntry{Op: op, Input: in, Basis: basis}
	if out != nil {
		entry.Output = out
	}
	if err != nil {
		if de, ok := AsDomainError(err); ok {
			entry.Error = map[string]string{"code": de.Code.String(), "message": de.Msg}
		} else {
			entry.Error = map[string]string{"code": ErrCodeGeneric.String(), "message": err.Error()}
		}
	}
	_ = e.audit.Append(entry)
}
