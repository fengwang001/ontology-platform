package ontology

import (
	"sort"
	"sync"
)

// EventStore 是追加式事件存储。Append 在互斥锁下分配全局递增
// 的 Seq，因此并发追加等价于按某个全局顺序串行执行。
// 每个实例的事件按 (Time, Seq) 保持有序，使重建可以按逻辑时刻
// 二分定位窗口，而不必扫描全部历史。
type EventStore struct {
	mu         sync.RWMutex
	seq        uint64
	events     []Event
	byInstance map[string][]Event
	// onAppend 在追加提交后（锁内）回调，用于检查点失效。
	onAppend func(Event)
}

// NewEventStore 创建空事件存储。
func NewEventStore() *EventStore {
	return &EventStore{byInstance: make(map[string][]Event)}
}

// SetAppendHook 注册追加回调（重建器用于检查点失效）。
func (s *EventStore) SetAppendHook(hook func(Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAppend = hook
}

// Append 追加一条事件并返回分配了 Seq 的副本。
func (s *EventStore) Append(ev Event) Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	ev.Seq = s.seq
	s.events = append(s.events, ev)
	l := s.byInstance[ev.InstanceID]
	// 按 (Time, Seq) 有序插入；新事件 Seq 最大，同刻事件排在最后。
	i := sort.Search(len(l), func(i int) bool { return l[i].Time > ev.Time })
	l = append(l, Event{})
	copy(l[i+1:], l[i:])
	l[i] = ev
	s.byInstance[ev.InstanceID] = l
	if s.onAppend != nil {
		s.onAppend(ev)
	}
	return ev
}

// Events 返回实例事件的一致快照（按 (Time, Seq) 规范顺序）。
func (s *EventStore) Events(instanceID string) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.byInstance[instanceID]
	out := make([]Event, len(src))
	copy(out, src)
	return out
}

// ReadWindow 在读锁内以 fn 暴露实例事件的有序切片（按 (Time, Seq)）。
// fn 不得保留切片引用；需要的数据应在 fn 内拷贝。
// 与 Append 的锁顺序一致（store 锁 -> 回调内的其他锁），不会死锁。
func (s *EventStore) ReadWindow(instanceID string, fn func(sorted []Event)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(s.byInstance[instanceID])
}

// Len 返回全局事件总数。
func (s *EventStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
}
