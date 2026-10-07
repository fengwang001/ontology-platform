package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// SnapshotInterval 是相邻快照之间的记录数。区间重放的扫描量
// 不超过 (SnapshotInterval + 区间长度)，与历史总记录数无关。
const SnapshotInterval = 64

// Executor 负责动作事务的执行，并与审计存证模块协作，
// 保证「审计记录」与「实际状态变更」同生同灭。
type Executor struct {
	mu     sync.Mutex
	store  *AuditStore
	state  map[string]string // 当前（订正后视图）实例状态
	latest map[string]int    // 每个实例最近一次有效提交动作的序号
}

// NewExecutor 基于审计序列创建执行器（可基于已有序列重开恢复）。
func NewExecutor(store *AuditStore) (*Executor, error) {
	e := &Executor{
		store:  store,
		state:  store.initialCopy(),
		latest: map[string]int{},
	}
	if err := e.restore(); err != nil {
		return nil, err
	}
	return e, nil
}

// ExecuteAction 原子执行一次多实例写入动作。
//
// 参数非法（目标实例不存在）最先报错，优先级高于审计故障。
// 审计记录先写入成功，随后才发布内存状态变更（同一把锁，对外等价原子）：
// 审计失败则状态原样不动、且不占用序号，杜绝单边发生。
func (e *Executor) ExecuteAction(actionID string, writes map[string]string, rollback bool) (*Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(writes) == 0 {
		return nil, &InvalidInputError{msg: "writes must not be empty"}
	}
	for inst := range writes {
		if _, ok := e.state[inst]; !ok {
			return nil, &InvalidInputError{msg: fmt.Sprintf("target instance %q does not exist", inst)}
		}
	}

	changes := e.changesFromWrites(writes, rollback)
	outcome := Committed
	if rollback {
		outcome = RolledBack
	}

	rec, err := e.store.append(KindAction, outcome, actionID, changes, 0)
	if err != nil {
		return nil, err // AuditWriteError：状态未触碰，无序号占用
	}

	if outcome == Committed {
		for _, c := range changes {
			e.state[c.Instance] = c.After
			e.latest[c.Instance] = rec.Seq
		}
	}
	e.maybeSnapshot(rec.Seq)
	return rec, nil
}

// Correct 追加一条订正记录。
//
// 参数非法优先：被订正序号不存在、被订正序号本身是订正记录、
// 订正实例不在原动作涉及集合内、订正集合为空。
func (e *Executor) Correct(actionID string, correctionOf int, corrected map[string]string) (*Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(corrected) == 0 {
		return nil, &InvalidInputError{msg: "correction must contain at least one instance"}
	}
	target, ok := e.store.viewGet(correctionOf)
	if !ok {
		return nil, &InvalidInputError{msg: fmt.Sprintf("corrected seq %d does not exist", correctionOf)}
	}
	if target.Kind == KindCorrection {
		return nil, &InvalidInputError{msg: fmt.Sprintf("seq %d is itself a correction record", correctionOf)}
	}
	involved := map[string]bool{}
	for _, c := range target.Changes {
		involved[c.Instance] = true
	}
	for inst := range corrected {
		if !involved[inst] {
			return nil, &InvalidInputError{msg: fmt.Sprintf("instance %q not involved in seq %d", inst, correctionOf)}
		}
	}

	keys := make([]string, 0, len(corrected))
	for k := range corrected {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	changes := make([]Change, 0, len(keys))
	for _, inst := range keys {
		var before string
		for _, c := range target.Changes {
			if c.Instance == inst {
				before = c.Before
				break
			}
		}
		changes = append(changes, Change{Instance: inst, Before: before, After: corrected[inst]})
	}

	rec, err := e.store.append(KindCorrection, Committed, actionID, changes, correctionOf)
	if err != nil {
		return nil, err
	}

	// 仅当被订正动作当前仍是该实例「最新有效写入」时才调整活状态；
	// 否则该值早已被后续动作覆盖，订正不改变当前值（但仍影响历史重建）。
	for _, c := range changes {
		if e.latest[c.Instance] == correctionOf {
			e.state[c.Instance] = c.After // store 已把它登记为该动作最新订正值
		}
	}
	e.maybeSnapshot(rec.Seq)
	return rec, nil
}

// StateAt 返回序号 r 处（订正后视图）全部实例状态深拷贝。
func (e *Executor) StateAt(r int) map[string]string {
	return NewReplayer(e.store).StateAt(r)
}

func (e *Executor) changesFromWrites(writes map[string]string, rollback bool) []Change {
	keys := make([]string, 0, len(writes))
	for k := range writes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	changes := make([]Change, 0, len(keys))
	for _, inst := range keys {
		before := e.state[inst]
		after := writes[inst]
		if rollback {
			after = before // 回退记录 Before==After，明确未生效
		}
		changes = append(changes, Change{Instance: inst, Before: before, After: after})
	}
	return changes
}

func (e *Executor) maybeSnapshot(seq int) {
	_ = seq // 执行器无需维护快照；重放器依据审计序列在间隔边界自建并缓存
}

// restore 重放已有审计序列重建内存状态，并校验哈希链。
func (e *Executor) restore() error {
	records := e.store.viewAll()
	latest := map[string]int{}
	state := e.store.initialCopy()
	rp := NewReplayer(e.store)

	for _, rec := range records {
		rp.apply(rec, state, latest)
		if rec.Kind == KindCorrection {
			// 订正不改 latest；但若被订正动作仍是某实例最新动作，
			// 则用其最新订正值就地更新当前值。
			for _, c := range rec.Changes {
				if latest[c.Instance] == rec.CorrectionOf {
					if v, ok := e.store.correctedAfter(rec.CorrectionOf, c.Instance); ok {
						state[c.Instance] = v
					}
				}
			}
		}
	}
	e.latest = latest
	e.state = state
	if n := len(records); n > 0 {
		e.maybeSnapshot(n)
	}
	return e.store.verifyHashChain()
}
