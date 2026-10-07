package ontology

import (
	"sync"
	"time"
)

// EventKind 是决策日志中的事件类别。
type EventKind string

const (
	EventAdmit     EventKind = "ADMIT"     // 请求获批，进入进行中并占用名额
	EventReject    EventKind = "REJECT"    // 请求被拒绝，不产生任何外部状态变化
	EventHeartbeat EventKind = "HEARTBEAT" // 进行中请求续期中断租约
	EventCommit    EventKind = "COMMIT"    // 进行中请求成功，名额转为已确认关联
	EventRollback  EventKind = "ROLLBACK"  // 进行中请求显式失败，名额原子释放
	EventExpire    EventKind = "EXPIRE"    // 心跳租约到期，名额原子释放
)

// Event 是一条不可变的决策记录。所有事件都在 Guard 的串行点内追加，
// 因而连续的序列号即等价串行顺序，可完整重放核验。
type Event struct {
	Seq         int64        `json:"seq"`
	RequestID   string       `json:"request_id"`
	Scope       ScopeKey     `json:"scope"`
	Kind        EventKind    `json:"kind"`
	Reason      RejectReason `json:"reason,omitempty"`
	Baseline    int64        `json:"baseline"`         // 判定时作用域当前版本
	ObservedVer int64        `json:"observed_version"` // 请求携带的基线版本
	Confirmed   int          `json:"confirmed"`        // 判定后已确认关联数
	InFlight    int          `json:"in_flight"`        // 判定后进行中占用数
	Reserve     bool         `json:"reserve"`          // 该事件后请求是否占用名额
	At          time.Time    `json:"at"`
	Source      string       `json:"source,omitempty"`
	Target      string       `json:"target,omitempty"`
}

// Journal 是仅供核验使用的只追加决策日志，不是对外暴露的业务状态：
// 它不参与名额计算，删除/清空它不影响判定结果；它是"判定依据"的证据。
type Journal struct {
	mu     sync.Mutex
	events []Event
}

func NewJournal() *Journal {
	return &Journal{}
}

// Append 原子追加一条事件并返回其 1 起、单调连续的序列号。
func (j *Journal) Append(e Event) int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	e.Seq = int64(len(j.events)) + 1
	j.events = append(j.events, e)
	return e.Seq
}

// Events 返回日志的快照副本。
func (j *Journal) Events() []Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Event, len(j.events))
	copy(out, j.events)
	return out
}

// Len 返回当前事件总数。
func (j *Journal) Len() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.events)
}
