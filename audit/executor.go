package audit

import (
	"fmt"
	"sync"
)

// Executor 负责动作事务与订正请求的执行。
//
// 每个对象类型拥有一把串行锁，使得该类型上所有“执行/订正”在全局
// 上等价于某个串行顺序逐一处理；审计序号分配与状态提交在同一临界
// 区内完成，从而保证审计记录与状态变更同生同灭。
type Executor struct {
	store *StateStore
	log   *AuditLog

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewExecutor 组装执行器。
func NewExecutor(store *StateStore, log *AuditLog) *Executor {
	return &Executor{store: store, log: log, locks: make(map[string]*sync.Mutex)}
}

func (e *Executor) typeLock(typeName string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	mu := e.locks[typeName]
	if mu == nil {
		mu = &sync.Mutex{}
		e.locks[typeName] = mu
	}
	return mu
}

// RegisterInstance 注册一个新实例并写入初始值。该注册同样是一次
// 经过审计的状态变更（占用该类型的一个序号），从而保证仅凭审计
// 序列即可独立重建出包含初始值在内的全部历史。
func (e *Executor) RegisterInstance(typeName, instance, initialValue string) (Record, error) {
	if typeName == "" || instance == "" {
		return Record{}, &IllegalRequestError{Reason: "empty type/instance"}
	}

	mu := e.typeLock(typeName)
	mu.Lock()
	defer mu.Unlock()

	txn := e.store.Begin(typeName)
	if txn.Exists(instance) {
		txn.Rollback()
		return Record{}, &IllegalRequestError{
			Reason: fmt.Sprintf("instance %q already exists", instance),
		}
	}
	txn.Write(instance, initialValue)

	rec, err := e.log.Append(typeName, appendInput{
		kind:     KindAction,
		actionID: "register:" + instance,
		changes: []Change{{
			Instance: instance,
			Before:   "",
			After:    initialValue,
		}},
	})
	if err != nil {
		txn.Rollback()
		return Record{}, &AuditWriteError{ActionID: "register:" + instance, Cause: err}
	}
	txn.Commit()
	return rec, nil
}

// Execute 执行一次动作。
//
// fn 在事务私有视图上产生写入意图，并可返回 ErrActionRolledBack
// 要求业务回退。处理次序：
//  1. 参数非法（目标实例不存在等）——最先报告；
//  2. 业务回退：占用一个序号，写入 Before==After 的回退记录，
//     状态不变；
//  3. 审计写入失败：返回 AuditWriteError 并丢弃暂存（不占序号）；
//  4. 成功提交：审计落盘后状态在同一临界区提交。
func (e *Executor) Execute(act Action, fn func(txn *Txn) error) (Record, error) {
	if act.ActionID == "" {
		return Record{}, &IllegalRequestError{Reason: "empty action id"}
	}
	if act.TypeName == "" {
		return Record{}, &IllegalRequestError{Reason: "empty type name"}
	}

	mu := e.typeLock(act.TypeName)
	mu.Lock()
	defer mu.Unlock()

	txn := e.store.Begin(act.TypeName)

	// 参数非法优先：动作显式声明的目标实例必须全部存在。
	for _, w := range act.Writes {
		if !txn.Exists(w.Instance) {
			txn.Rollback()
			return Record{}, &IllegalRequestError{
				Reason: fmt.Sprintf("target instance %q does not exist", w.Instance),
			}
		}
	}
	// 若动作通过 fn 直接写入，这里给出便捷的声明式执行：
	for _, w := range act.Writes {
		txn.Write(w.Instance, w.Value)
	}

	rollback := false
	if fn != nil {
		if err := fn(txn); err != nil {
			if !isRollbackSentinel(err) {
				// 业务代码返回的参数非法仍属于第一优先级。
				if illegal, ok := err.(*IllegalRequestError); ok { //nolint:errorlint
					txn.Rollback()
					return Record{}, illegal
				}
				txn.Rollback()
				return Record{}, err
			}
			rollback = true
		}
	}

	// 参数非法优先：实际写入的目标实例必须全部存在（不允许借动作
	// 隐式创建实例）。
	for _, inst := range txn.WrittenInstances() {
		if _, existed := txn.Baseline(inst); !existed {
			txn.Rollback()
			return Record{}, &IllegalRequestError{
				Reason: fmt.Sprintf("target instance %q does not exist", inst),
			}
		}
	}

	changes := buildChanges(txn)

	if rollback {
		// 回退记录：Before 与 After 必须相同，显式标记“未生效”。
		rb := make([]Change, len(changes))
		for i, c := range changes {
			rb[i] = Change{Instance: c.Instance, Before: c.Before, After: c.Before}
		}
		rec, err := e.log.Append(act.TypeName, appendInput{
			kind:     KindRollback,
			actionID: act.ActionID,
			changes:  rb,
		})
		if err != nil {
			// 回退记录都写不进去：没有序号、没有状态变更，整体未提交。
			txn.Rollback()
			return Record{}, &AuditWriteError{ActionID: act.ActionID, Cause: err}
		}
		// 丢弃全部暂存：状态维持回退前值。
		txn.Rollback()
		return rec, nil
	}

	rec, err := e.log.Append(act.TypeName, appendInput{
		kind:     KindAction,
		actionID: act.ActionID,
		changes:  changes,
	})
	if err != nil {
		// 审计缺失 ⇒ 动作未提交 ⇒ 撤销全部状态变更。
		txn.Rollback()
		return Record{}, &AuditWriteError{ActionID: act.ActionID, Cause: err}
	}

	// 审计已不可变地存在，此刻才让状态对外可见——同一把类型锁内
	// 完成，二者不会单独出现。
	txn.Commit()
	return rec, nil
}

// Correct 在审计序列尾部追加一条订正记录。
//
// 参数非法优先级最高：
//   - 被订正序号必须存在；
//   - 被订正记录必须是已提交动作（不能是回退记录，也不能是订正
//     记录，订正不得指向另一条订正）；
//   - 订正必须给出与原记录完全相同的实例集合。
func (e *Executor) Correct(typeName, correctionID string, targetSeq int64, corrected map[string]string) (Record, error) {
	if typeName == "" || correctionID == "" {
		return Record{}, &IllegalRequestError{Reason: "empty type/correction id"}
	}

	mu := e.typeLock(typeName)
	mu.Lock()
	defer mu.Unlock()

	target, ok := e.log.At(typeName, targetSeq)
	if !ok {
		return Record{}, &IllegalRequestError{
			Reason: fmt.Sprintf("corrected seq %d does not exist", targetSeq),
		}
	}
	if target.Kind == KindCorrection {
		return Record{}, &IllegalRequestError{
			Reason: fmt.Sprintf("seq %d is itself a correction record", targetSeq),
		}
	}
	if target.Kind != KindAction {
		return Record{}, &IllegalRequestError{
			Reason: fmt.Sprintf("seq %d is not a committed action", targetSeq),
		}
	}
	if len(corrected) != len(target.Changes) {
		return Record{}, &IllegalRequestError{
			Reason: fmt.Sprintf("correction covers %d instances, original covered %d",
				len(corrected), len(target.Changes)),
		}
	}

	// 订正记录中 Before=原记录写入后的值，After=订正后的值。
	// 原记录在序列中原样保留，不被物理替换。
	changes := make([]Change, 0, len(target.Changes))
	for _, c := range target.Changes {
		after, ok := corrected[c.Instance]
		if !ok {
			return Record{}, &IllegalRequestError{
				Reason: fmt.Sprintf("correction missing instance %q of original seq %d", c.Instance, targetSeq),
			}
		}
		changes = append(changes, Change{Instance: c.Instance, Before: c.After, After: after})
	}

	rec, err := e.log.Append(typeName, appendInput{
		kind:      KindCorrection,
		actionID:  correctionID,
		changes:   changes,
		targetSeq: targetSeq,
	})
	if err != nil {
		return Record{}, &AuditWriteError{ActionID: correctionID, Cause: err}
	}

	// 订正同样要与“订正后的当前状态”同生同灭：在同一临界区内把
	// 订正值应用到活动状态。为了与重放语义（“订正替换原动作增量、
	// 后续正常写入优先”）一致，只覆盖当前值仍等于原动作写入值的
	// 实例；若其间已有更晚的提交改写过该实例，则保持该更晚的值。
	txn := e.store.Begin(typeName)
	for _, c := range changes {
		if cur, ok := txn.Read(c.Instance); ok && cur == c.Before {
			txn.Write(c.Instance, c.After)
		}
	}
	txn.Commit()

	return rec, nil
}

// buildChanges 从事务实际写入过的实例集合计算确定性排序后的
// 变更前后值（Before 取 Begin 时刻快照值）。
func buildChanges(txn *Txn) []Change {
	instances := txn.WrittenInstances()
	if len(instances) == 0 {
		return nil
	}

	changes := make([]Change, 0, len(instances))
	for _, inst := range instances {
		before, _ := txn.Baseline(inst)
		after, _ := txn.Read(inst)
		changes = append(changes, Change{
			Instance: inst,
			Before:   before,
			After:    after,
		})
	}
	return changes
}

func isRollbackSentinel(err error) bool {
	return err == ErrActionRolledBack
}
