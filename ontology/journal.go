package ontology

import (
	"fmt"
	"sync"
)

// CompensationState 是补偿动作的生命周期状态。
type CompensationState int

const (
	// StatePending 已登记但尚未提交任何副作用。
	StatePending CompensationState = iota
	// StateInProgress 已提交部分副作用，尚未完成。
	StateInProgress
	// StateCompleted 全部副作用已提交。
	StateCompleted
	// StateAbandoned 原始动作在补偿开始前被撤销，补偿被放弃。
	StateAbandoned
	// StateFailed 发生已分类错误，停在确定的副作用边界上。
	StateFailed
)

func (s CompensationState) String() string {
	switch s {
	case StatePending:
		return "Pending"
	case StateInProgress:
		return "InProgress"
	case StateCompleted:
		return "Completed"
	case StateAbandoned:
		return "Abandoned"
	case StateFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// EffectRecord 是一项副作用的持久化执行记录。
type EffectRecord struct {
	// Key 是幂等键：actionExecutionID + 副作用序号，全局唯一。
	Key       string
	Effect    SideEffect
	Committed bool
}

// CompensationRecord 是一次补偿动作的持久化执行记录（历史记录）。
type CompensationRecord struct {
	ActionExecutionID string
	ActionType        string
	State             CompensationState
	Effects           []EffectRecord
	Err               *ConsumeError
}

// CommittedCount 返回已提交的副作用数量。
func (r *CompensationRecord) CommittedCount() int {
	n := 0
	for _, e := range r.Effects {
		if e.Committed {
			n++
		}
	}
	return n
}

// Journal 是补偿执行历史（预写日志）的持久化存储。
// 消费端故障重启后，新的消费实例在同一 Journal 上续作。
type Journal struct {
	mu      sync.Mutex
	records map[string]*CompensationRecord // key: actionExecutionID
}

// NewJournal 构造空日志。
func NewJournal() *Journal {
	return &Journal{records: make(map[string]*CompensationRecord)}
}

// Get 返回指定动作执行的补偿记录副本；不存在时 ok=false。
func (j *Journal) Get(actionExecutionID string) (CompensationRecord, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	r, found := j.records[actionExecutionID]
	if !found {
		return CompensationRecord{}, false
	}
	return cloneRecord(r), true
}

// put 整体写入记录（调用方需持有消费端全局锁保证串行）。
func (j *Journal) put(r CompensationRecord) {
	j.mu.Lock()
	defer j.mu.Unlock()
	c := cloneRecord(&r)
	j.records[r.ActionExecutionID] = &c
}

// delete 删除记录，仅用于测试注入“历史记录缺失”故障。
func (j *Journal) delete(actionExecutionID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.records, actionExecutionID)
}

// corrupt 清空记录的副作用明细但保留记录本身，仅用于测试注入
// “历史记录部分缺失”故障。
func (j *Journal) corrupt(actionExecutionID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if r, ok := j.records[actionExecutionID]; ok {
		r.Effects = nil
	}
}

// effectKey 生成副作用的幂等键。
func effectKey(actionExecutionID string, index int) string {
	return fmt.Sprintf("%s#%d", actionExecutionID, index)
}

func cloneRecord(r *CompensationRecord) CompensationRecord {
	out := *r
	out.Effects = make([]EffectRecord, len(r.Effects))
	copy(out.Effects, r.Effects)
	return out
}
