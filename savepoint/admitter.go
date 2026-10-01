package savepoint

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// DefaultMaxParallelism 新增算子的缺省最大并行度。
const DefaultMaxParallelism = 128

// Admitter 保存点恢复准入器，全部方法可并发调用。
type Admitter struct {
	mu         sync.Mutex
	savepoints map[string]Savepoint
	// jobs 记录已占用作业标识及其引用的保存点。
	jobs map[string]string
	// refs 记录保存点被存活作业引用的次数。
	refs map[string]int
}

// NewAdmitter 创建准入器。
func NewAdmitter() *Admitter {
	return &Admitter{
		savepoints: make(map[string]Savepoint),
		jobs:       make(map[string]string),
		refs:       make(map[string]int),
	}
}

// RegisterSavepoint 登记保存点；登记后不可变，重复登记报错。
func (a *Admitter) RegisterSavepoint(sp Savepoint) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if sp.ID == "" {
		return errors.New("savepoint id is empty")
	}
	if _, ok := a.savepoints[sp.ID]; ok {
		return fmt.Errorf("savepoint %q already registered", sp.ID)
	}
	if err := validateSavepoint(sp); err != nil {
		return err
	}
	a.savepoints[sp.ID] = cloneSavepoint(sp)
	return nil
}

// Restore 对作业图做恢复准入判定。成功返回计划并占用作业标识；
// 拒绝时不改变任何状态。
func (a *Admitter) Restore(jobID, savepointID string, allowDiscard bool, graph JobGraph) (*Plan, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 1. 保存点不存在。
	sp, ok := a.savepoints[savepointID]
	if !ok {
		return nil, &RejectError{Reason: ReasonSavepointNotFound, Objects: []string{savepointID}}
	}
	// 2. 作业标识已被占用。
	if _, ok := a.jobs[jobID]; ok {
		return nil, &RejectError{Reason: ReasonJobIDInUse, Objects: []string{jobID}}
	}

	spOps := make(map[string]SavepointOperator, len(sp.Operators))
	for _, op := range sp.Operators {
		spOps[op.ID] = op
	}

	// 解析每个新算子的承接对象与生效最大并行度。
	type resolved struct {
		op     JobOperator
		source string // 承接的保存点算子标识；新增算子为空
		maxPar int
	}
	res := make([]resolved, 0, len(graph.Operators))

	// 3. 作业图非法。
	var invalid []string
	seenIDs := make(map[string]bool)
	for _, op := range graph.Operators {
		if op.ID == "" {
			invalid = append(invalid, "operator with empty id")
			continue
		}
		if seenIDs[op.ID] {
			invalid = append(invalid, fmt.Sprintf("operator %q: duplicate id", op.ID))
			continue
		}
		seenIDs[op.ID] = true

		source := ""
		if op.SourceID != nil {
			if _, ok := spOps[*op.SourceID]; !ok {
				invalid = append(invalid, fmt.Sprintf("operator %q: source %q not in savepoint", op.ID, *op.SourceID))
				continue
			}
			source = *op.SourceID
		} else if _, ok := spOps[op.ID]; ok {
			source = op.ID
		}

		maxPar := DefaultMaxParallelism
		if source != "" {
			maxPar = spOps[source].MaxParallelism
		}
		if op.MaxParallelism != nil {
			maxPar = *op.MaxParallelism
		}
		if op.Parallelism <= 0 {
			invalid = append(invalid, fmt.Sprintf("operator %q: non-positive parallelism %d", op.ID, op.Parallelism))
			continue
		}
		if op.Parallelism > maxPar {
			invalid = append(invalid, fmt.Sprintf("operator %q: parallelism %d exceeds max parallelism %d", op.ID, op.Parallelism, maxPar))
			continue
		}
		res = append(res, resolved{op: op, source: source, maxPar: maxPar})
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		return nil, &RejectError{Reason: ReasonInvalidGraph, Objects: invalid}
	}

	// 4. 同一保存点算子被多个新算子承接。
	claimCount := make(map[string]int)
	for _, r := range res {
		if r.source != "" {
			claimCount[r.source]++
		}
	}
	var dupClaims []string
	for id, n := range claimCount {
		if n > 1 {
			dupClaims = append(dupClaims, id)
		}
	}
	if len(dupClaims) > 0 {
		sort.Strings(dupClaims)
		return nil, &RejectError{Reason: ReasonDuplicateClaim, Objects: dupClaims}
	}

	// 5. 承接者最大并行度必须等于保存点的 m。
	var mismatched []string
	for _, r := range res {
		if r.source != "" && r.maxPar != spOps[r.source].MaxParallelism {
			mismatched = append(mismatched, fmt.Sprintf("operator %q: max parallelism %d != savepoint %d",
				r.op.ID, r.maxPar, spOps[r.source].MaxParallelism))
		}
	}
	if len(mismatched) > 0 {
		sort.Strings(mismatched)
		return nil, &RejectError{Reason: ReasonMaxParallelismMismatch, Objects: mismatched}
	}

	// 6. 未被承接的保存点算子。
	claimed := make(map[string]bool)
	for _, r := range res {
		if r.source != "" {
			claimed[r.source] = true
		}
	}
	var unclaimed []string
	for _, op := range sp.Operators {
		if !claimed[op.ID] {
			unclaimed = append(unclaimed, op.ID)
		}
	}
	if len(unclaimed) > 0 && !allowDiscard {
		sort.Strings(unclaimed)
		return nil, &RejectError{Reason: ReasonUnclaimedOperators, Objects: unclaimed}
	}
	sort.Strings(unclaimed)

	// 7. 同名状态项兼容性：种类须相同，值类型相同或 int 变宽为 long。
	var incompatible []string
	for _, r := range res {
		if r.source == "" {
			continue
		}
		spItems := itemIndex(spOps[r.source].States)
		for _, it := range r.op.States {
			old, ok := spItems[it.Name]
			if !ok {
				continue
			}
			if old.Kind != it.Kind || !typeCompatible(old.Type, it.Type) {
				incompatible = append(incompatible, fmt.Sprintf("%s.%s", r.op.ID, it.Name))
			}
		}
	}
	if len(incompatible) > 0 {
		sort.Strings(incompatible)
		return nil, &RejectError{Reason: ReasonIncompatibleStateItems, Objects: incompatible}
	}

	// 8. 被承接算子中新图缺失的同名状态项。
	var missing []string
	for _, r := range res {
		if r.source == "" {
			continue
		}
		newItems := make(map[string]bool)
		for _, it := range r.op.States {
			newItems[it.Name] = true
		}
		for _, old := range spOps[r.source].States {
			if !newItems[old.Name] {
				missing = append(missing, fmt.Sprintf("%s.%s", r.op.ID, old.Name))
			}
		}
	}
	if len(missing) > 0 && !allowDiscard {
		sort.Strings(missing)
		return nil, &RejectError{Reason: ReasonMissingStateItems, Objects: missing}
	}
	sort.Strings(missing)

	// 全部校验通过，生成计划并原子占用作业标识。
	plan := &Plan{
		JobID:             jobID,
		SavepointID:       savepointID,
		DroppedOperators:  unclaimed,
		DroppedStateItems: missing,
	}
	for _, r := range res {
		opPlan := OperatorPlan{
			OperatorID:     r.op.ID,
			Source:         r.source,
			MaxParallelism: r.maxPar,
		}
		if r.source == "" {
			opPlan.Action = ActionStartEmpty
		} else {
			spItems := itemIndex(spOps[r.source].States)
			opPlan.Action = ActionRestoreDirect
			opPlan.ItemActions = make(map[string]Action)
			names := make([]string, 0, len(r.op.States))
			for _, it := range r.op.States {
				names = append(names, it.Name)
			}
			sort.Strings(names)
			for _, name := range names {
				it := findItem(r.op.States, name)
				old, ok := spItems[name]
				switch {
				case !ok:
					opPlan.ItemActions[name] = ActionStartEmpty
				case old.Type == TypeInt && it.Type == TypeLong:
					opPlan.ItemActions[name] = ActionRestoreWiden
					opPlan.Action = ActionRestoreWiden
				default:
					opPlan.ItemActions[name] = ActionRestoreDirect
				}
			}
		}
		plan.Operators = append(plan.Operators, opPlan)
	}
	sort.Slice(plan.Operators, func(i, j int) bool {
		return plan.Operators[i].OperatorID < plan.Operators[j].OperatorID
	})

	a.jobs[jobID] = savepointID
	a.refs[savepointID]++
	return plan, nil
}

// itemIndex 按名称索引状态项。
func itemIndex(items []StateItem) map[string]StateItem {
	out := make(map[string]StateItem, len(items))
	for _, it := range items {
		out[it.Name] = it
	}
	return out
}

func findItem(items []StateItem, name string) StateItem {
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	return StateItem{}
}

// typeCompatible 值类型相同，或由 int 变宽为 long。
func typeCompatible(old, new ValueType) bool {
	if old == new {
		return true
	}
	return old == TypeInt && new == TypeLong
}

// Stop 停止作业，释放作业标识与其对保存点的引用。
func (a *Admitter) Stop(jobID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	spID, ok := a.jobs[jobID]
	if !ok {
		return fmt.Errorf("job %q not running", jobID)
	}
	delete(a.jobs, jobID)
	a.refs[spID]--
	if a.refs[spID] <= 0 {
		delete(a.refs, spID)
	}
	return nil
}

// DeleteSavepoint 删除保存点；被存活作业引用时拒绝。
func (a *Admitter) DeleteSavepoint(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.savepoints[id]; !ok {
		return fmt.Errorf("savepoint %q not found", id)
	}
	if a.refs[id] > 0 {
		return fmt.Errorf("savepoint %q still referenced by live jobs", id)
	}
	delete(a.savepoints, id)
	return nil
}

func validateSavepoint(sp Savepoint) error {
	seen := make(map[string]bool)
	for _, op := range sp.Operators {
		if op.ID == "" {
			return errors.New("savepoint operator id is empty")
		}
		if seen[op.ID] {
			return fmt.Errorf("duplicate savepoint operator %q", op.ID)
		}
		seen[op.ID] = true
		if op.MaxParallelism <= 0 {
			return fmt.Errorf("savepoint operator %q has non-positive max parallelism", op.ID)
		}
		items := make(map[string]bool)
		for _, it := range op.States {
			if it.Name == "" {
				return fmt.Errorf("savepoint operator %q has empty state name", op.ID)
			}
			if items[it.Name] {
				return fmt.Errorf("savepoint operator %q duplicates state %q", op.ID, it.Name)
			}
			items[it.Name] = true
			if !validKind(it.Kind) || !validType(it.Type) {
				return fmt.Errorf("savepoint operator %q state %q has invalid kind/type", op.ID, it.Name)
			}
		}
	}
	return nil
}

func validKind(k StateKind) bool {
	return k == KindValue || k == KindList || k == KindMap
}

func validType(t ValueType) bool {
	return t == TypeInt || t == TypeLong || t == TypeString
}

func cloneSavepoint(sp Savepoint) Savepoint {
	out := Savepoint{ID: sp.ID, Operators: make([]SavepointOperator, len(sp.Operators))}
	for i, op := range sp.Operators {
		states := make([]StateItem, len(op.States))
		copy(states, op.States)
		out.Operators[i] = SavepointOperator{ID: op.ID, MaxParallelism: op.MaxParallelism, States: states}
	}
	return out
}
