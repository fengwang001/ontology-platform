package saga

import (
	"reflect"

	"ontology/journal"
)

// State 返回实例当前状态（归约自日志）。
func (o *Orchestrator) State(id string) (State, error) {
	o.mu.RLock()
	it, ok := o.insts[id]
	o.mu.RUnlock()
	if !ok {
		return State{}, ErrInstanceNotFound
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	return o.snapshotLocked(id, it), nil
}

// Calls 返回某实例各动作被真实调用的次数（副本）。
func (o *Orchestrator) Calls(id string) (Calls, error) {
	o.mu.RLock()
	it, ok := o.insts[id]
	o.mu.RUnlock()
	if !ok {
		return Calls{}, ErrInstanceNotFound
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	cp := Calls{Forward: map[string]int{}, Compensate: map[string]int{}}
	for k, v := range it.calls.Forward {
		cp.Forward[k] = v
	}
	for k, v := range it.calls.Compensate {
		cp.Compensate[k] = v
	}
	cp.ForwardAll = it.calls.ForwardAll
	cp.CompensateAll = it.calls.CompensateAll
	return cp, nil
}

// Reconstruct 只依据给定日志与步骤定义重建实例状态（离线事实来源核对）。
func Reconstruct(id string, recs []journal.Record, steps int) State {
	cp := make([]journal.Record, len(recs))
	copy(cp, recs)
	return reconstruct(id, cp, steps)
}

// SelfCheck 一次性核验：
//  1. 该实例日志中每个步骤的记录序列合法；
//  2. 在线状态与「只从日志重建」的状态逐字段相同。
func (o *Orchestrator) SelfCheck(id string) error {
	o.mu.RLock()
	it, ok := o.insts[id]
	o.mu.RUnlock()
	if !ok {
		return ErrInstanceNotFound
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	recs := o.journal.Read(id)
	if err := ValidateRecordSequence(recs, len(it.steps)); err != nil {
		return err
	}
	online := o.snapshotLocked(id, it)
	rebuilt := reconstruct(id, recs, len(it.steps))
	if !reflect.DeepEqual(online, rebuilt) {
		return &ConsistencyError{Online: online, Rebuilt: rebuilt}
	}
	return nil
}

func (o *Orchestrator) snapshotLocked(id string, it *inst) State {
	return reconstruct(id, o.journal.Read(id), len(it.steps))
}

// ConsistencyError 表示在线状态与日志重建结果不一致（自检失败）。
type ConsistencyError struct {
	Online  State
	Rebuilt State
}

func (e *ConsistencyError) Error() string {
	return "saga: online state differs from journal reconstruction"
}
