package authz

import (
	"fmt"
	"sort"
	"sync"
)

// key 为权限规则的主体+标签维度。
type key struct {
	subject string
	label   string
}

// keyIndex 为单个 key 的已生效变更索引，按叠加顺序
// （生效时刻、提交时刻、固有 ID）有序。已生效记录不可变，
// 只能追加/插入新记录，保证历史查询的可重复读。
type keyIndex struct {
	entries []*Change
}

func (k *keyIndex) insert(c *Change) {
	i := sort.Search(len(k.entries), func(i int) bool {
		return lessOrder(*c, *k.entries[i])
	})
	k.entries = append(k.entries, nil)
	copy(k.entries[i+1:], k.entries[i:])
	k.entries[i] = c
}

// prefix 返回生效时刻不超过 at 的有序前缀。
func (k *keyIndex) prefix(at Time) []*Change {
	n := sort.Search(len(k.entries), func(i int) bool {
		return k.entries[i].EffectiveAt > at
	})
	return k.entries[:n]
}

// Timeline 为权限变更的生效时序管理器。所有方法可并发调用，
// 内部通过单一互斥锁串行化，任意并发调用的结果等价于某个
// 串行执行顺序。
type Timeline struct {
	mu       sync.Mutex
	now      Time
	earliest Time
	nextID   ChangeID
	// changes 保存全部已受理且未撤回的变更（含排队与已生效）。
	changes map[ChangeID]*Change
	// pending 为尚未到达生效时刻的排队变更。
	pending map[ChangeID]*Change
	// indexes 按 key 维护已生效变更的有序索引，使单次查询
	// 的开销与系统中累计变更总数无关。
	indexes map[key]*keyIndex
	log     *MemLogger
	extra   []Logger
}

// NewTimeline 创建 Timeline，now 为系统时钟的初始时刻，
// 同时构成系统已知的最早记录时刻。
func NewTimeline(now Time, loggers ...Logger) *Timeline {
	return &Timeline{
		now:      now,
		earliest: now,
		nextID:   1,
		changes:  map[ChangeID]*Change{},
		pending:  map[ChangeID]*Change{},
		indexes:  map[key]*keyIndex{},
		log:      &MemLogger{},
		extra:    loggers,
	}
}

// Submit 提交一条权限变更，返回分配的变更 ID；
// 若该变更为立即生效的 OpRevoke，同时返回后续变更的重估报告。
//
// 校验按固定优先级进行：先生效时刻/提交时刻次序，再目标存在性。
// 被拒绝的提交不改变任何排队状态与时钟。
func (t *Timeline) Submit(in ChangeInput) (ChangeID, *Reevaluation, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if in.EffectiveAt < in.SubmittedAt {
		t.record("submit", fmt.Sprintf("%+v", in), "", nil, ErrEffectiveBeforeSubmit)
		return 0, nil, ErrEffectiveBeforeSubmit
	}
	if in.Op == OpRevoke {
		target, ok := t.changes[in.Target]
		if !ok {
			t.record("submit", fmt.Sprintf("%+v", in), "", nil, ErrChangeNotFound)
			return 0, nil, ErrChangeNotFound
		}
		// 撤销变更归入被撤销目标的 key，与调用方填写的维度无关。
		in.Subject = target.Subject
		in.Label = target.Label
	}
	for _, d := range in.DependsOn {
		dep, ok := t.changes[d]
		if !ok || dep.Subject != in.Subject || dep.Label != in.Label {
			t.record("submit", fmt.Sprintf("%+v", in), "", nil, ErrChangeNotFound)
			return 0, nil, ErrChangeNotFound
		}
	}

	c := &Change{ChangeInput: in, ID: t.nextID}
	t.nextID++
	t.changes[c.ID] = c
	if in.SubmittedAt > t.now {
		t.now = in.SubmittedAt
	}
	if in.SubmittedAt < t.earliest {
		t.earliest = in.SubmittedAt
	}

	var reeval *Reevaluation
	if c.EffectiveAt <= t.now {
		reeval = t.promote(c)
	} else {
		t.pending[c.ID] = c
	}
	out := fmt.Sprintf("id=%d queued=%v", c.ID, reeval == nil && c.EffectiveAt > t.now)
	t.record("submit", fmt.Sprintf("%+v", in), out, []ChangeID{c.ID}, nil)
	return c.ID, reeval, nil
}

// Withdraw 撤回一条尚未生效的排队变更。
// 已生效变更只能由新变更覆盖或撤销，不得直接撤回。
func (t *Timeline) Withdraw(id ChangeID) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	in := fmt.Sprintf("id=%d", id)
	if _, ok := t.pending[id]; ok {
		delete(t.pending, id)
		delete(t.changes, id)
		t.record("withdraw", in, "withdrawn", []ChangeID{id}, nil)
		return nil
	}
	if _, ok := t.changes[id]; ok {
		t.record("withdraw", in, "", nil, ErrWithdrawEffective)
		return ErrWithdrawEffective
	}
	t.record("withdraw", in, "", nil, ErrChangeNotFound)
	return ErrChangeNotFound
}

// Decide 对 (subject, label) 在时刻 at 做访问判定。
// 排队中的变更对任何查询时刻均不可见；at 晚于当前时钟时
// 等价于对当前时刻的查询。
func (t *Timeline) Decide(subject, label string, at Time) (Decision, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	in := fmt.Sprintf("subject=%q label=%q at=%d", subject, label, at)
	if at < t.earliest {
		t.record("decide", in, "", nil, ErrQueryBeforeEarliest)
		return Decision{}, ErrQueryBeforeEarliest
	}
	var d Decision
	if idx, ok := t.indexes[key{subject, label}]; ok {
		d, _ = eval(idx.prefix(at))
	}
	t.record("decide", in, fmt.Sprintf("found=%v value=%q examined=%d", d.Found, d.Value, d.Examined), d.Basis, nil)
	return d, nil
}

// Advance 将系统时钟推进到 to，使所有生效时刻不超过 to 的
// 排队变更生效，并返回其中 OpRevoke 触发的重估报告。
func (t *Timeline) Advance(to Time) ([]Reevaluation, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	in := fmt.Sprintf("to=%d", to)
	if to < t.now {
		t.record("advance", in, "", nil, ErrClockRegression)
		return nil, ErrClockRegression
	}
	t.now = to
	var promoted []*Change
	for id, c := range t.pending {
		if c.EffectiveAt <= to {
			promoted = append(promoted, c)
			delete(t.pending, id)
		}
	}
	sort.Slice(promoted, func(i, j int) bool { return lessOrder(*promoted[i], *promoted[j]) })
	var reevals []Reevaluation
	var basis []ChangeID
	for _, c := range promoted {
		basis = append(basis, c.ID)
		if r := t.promote(c); r != nil {
			reevals = append(reevals, *r)
		}
	}
	t.record("advance", in, fmt.Sprintf("promoted=%v", basis), basis, nil)
	return reevals, nil
}

// Now 返回当前系统时钟。
func (t *Timeline) Now() Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.now
}

// Logs 返回全部调用日志（追加顺序即串行化顺序）。
func (t *Timeline) Logs() []LogEntry {
	return t.log.Entries()
}

// promote 将一条变更纳入其 key 的已生效索引；若为 OpRevoke，
// 计算后续依赖变更的重估报告。调用方须持有锁。
func (t *Timeline) promote(c *Change) *Reevaluation {
	k := key{c.Subject, c.Label}
	idx := t.indexes[k]
	if idx == nil {
		idx = &keyIndex{}
		t.indexes[k] = idx
	}
	idx.insert(c)
	if c.Op != OpRevoke {
		return nil
	}
	return t.reevaluate(idx, c)
}

// reevaluate 在撤销变更 r（目标 x）生效后，重新考察所有
// 生效时刻晚于 x 且传递依赖于 x 的后续变更，给出其在新基线
// 下是否仍然有效、效果是否改变的确定结论。
func (t *Timeline) reevaluate(idx *keyIndex, r *Change) *Reevaluation {
	x := t.changes[r.Target]
	report := &Reevaluation{RevokedBy: r.ID, Revoked: x.ID}
	// 重估前：撤销尚未生效的旧基线（索引中排除 r）。
	before := applicabilityWithout(idx.entries, r.ID)
	// 重估后：撤销生效的新基线（完整索引）。
	_, after := eval(idx.entries)
	for _, c := range idx.entries {
		if c.EffectiveAt <= x.EffectiveAt || c.ID == r.ID {
			continue
		}
		if !t.dependsOn(c, x.ID, map[ChangeID]bool{}) {
			continue
		}
		validBefore := before[c.ID]
		validAfter := after[c.ID]
		report.Items = append(report.Items, ReevalItem{
			ChangeID:     c.ID,
			StillValid:   validAfter,
			EffectChange: validBefore != validAfter,
		})
	}
	return report
}

// dependsOn 判断 c 是否传递依赖于目标变更。
func (t *Timeline) dependsOn(c *Change, target ChangeID, seen map[ChangeID]bool) bool {
	for _, d := range c.DependsOn {
		if d == target {
			return true
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		if dep, ok := t.changes[d]; ok && t.dependsOn(dep, target, seen) {
			return true
		}
	}
	return false
}

// eval 在有序变更记录上折叠出判定结果与适用集。
// 返回的 Decision.Examined 等于考察的记录数量。
func eval(entries []*Change) (Decision, map[ChangeID]bool) {
	byID := make(map[ChangeID]*Change, len(entries))
	revoked := map[ChangeID]bool{}
	for _, c := range entries {
		byID[c.ID] = c
		if c.Op == OpRevoke {
			revoked[c.Target] = true
		}
	}
	memo := map[ChangeID]bool{}
	var applicable func(c *Change) bool
	applicable = func(c *Change) bool {
		if v, ok := memo[c.ID]; ok {
			return v
		}
		memo[c.ID] = false // 环保护
		ok := !revoked[c.ID]
		if ok && c.Op == OpSet {
			for _, d := range c.DependsOn {
				dep, exists := byID[d]
				if !exists || !applicable(dep) {
					ok = false
					break
				}
			}
		}
		if ok && c.Op == OpRevoke {
			_, exists := byID[c.Target]
			ok = exists
		}
		memo[c.ID] = ok
		return ok
	}
	d := Decision{Examined: len(entries)}
	valid := map[ChangeID]bool{}
	for _, c := range entries {
		if !applicable(c) {
			continue
		}
		valid[c.ID] = true
		d.Basis = append(d.Basis, c.ID)
		if c.Op == OpSet {
			d.Found = true
			d.Value = c.Value
		}
	}
	return d, valid
}

// applicabilityWithout 返回在排除 excludeID 的记录后各变更的
// 适用性，用于构造“撤销尚未生效”的旧基线。
func applicabilityWithout(entries []*Change, excludeID ChangeID) map[ChangeID]bool {
	filtered := make([]*Change, 0, len(entries))
	for _, c := range entries {
		if c.ID != excludeID {
			filtered = append(filtered, c)
		}
	}
	_, valid := eval(filtered)
	return valid
}

// record 追加一条调用日志。调用方须持有锁。
func (t *Timeline) record(call, in, out string, basis []ChangeID, err error) {
	e := LogEntry{
		Seq:    len(t.log.entries) + 1,
		Call:   call,
		Input:  in,
		Output: out,
		Basis:  basis,
		Err:    err,
	}
	t.log.Log(e)
	for _, l := range t.extra {
		l.Log(e)
	}
}
