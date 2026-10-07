package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// SnapshotInterval 控制每追加多少个事件强制落一次快照，
// 保证任意两次快照之间的事件数有常数上界。
const SnapshotInterval = 64

// snapshot 是某一事件序号之后的物化状态。
type snapshot struct {
	seq   int
	state State
}

// stream 是单个实例的事件流及其快照索引。
type stream struct {
	events         []Event
	snapshots      []snapshot
	current        State
	orderViolation int // 首个违反“时刻严格递增”的事件下标，-1 表示无
	broken         bool
}

// Store 是事件存储：负责追加校验、快照维护与状态重建。
// 全部操作在单把互斥锁下串行化，因此任意并发调用都等价于
// 某个全局串行顺序（线性化点位于各自的临界区内）。
type Store struct {
	mu      sync.RWMutex
	rules   *RuleStore
	streams map[string]*stream
}

// NewStore 创建事件存储。
func NewStore(rules *RuleStore) *Store {
	return &Store{rules: rules, streams: make(map[string]*stream)}
}

// Append 校验并追加一个事件；校验失败时不产生任何可观察改动。
func (s *Store) Append(instanceID string, ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[instanceID]
	if !ok {
		st = &stream{orderViolation: -1}
		s.streams[instanceID] = st
	}
	if st.broken {
		return fmt.Errorf("ontology: 实例 %q 的事件流由导入产生且无法重放，禁止追加", instanceID)
	}
	if len(st.events) == 0 && ev.Kind != EvCreated {
		return ErrFirstEventNotCreated
	}
	if n := len(st.events); n > 0 && ev.Time <= st.events[n-1].Time {
		return fmt.Errorf("事件时刻 %d 未严格大于前一事件时刻 %d: %w",
			ev.Time, st.events[n-1].Time, ErrAmbiguousOrder)
	}
	// 在克隆状态上试算，成功才提交，保证失败追加无可观察改动。
	m := machine{typ: st.current.TypeID, props: make(map[string]Value, len(st.current.Props))}
	for k, v := range st.current.Props {
		m.props[k] = v
	}
	if err := m.apply(ev, s.rules, true, nil); err != nil {
		return err
	}
	ev.Seq = len(st.events)
	st.events = append(st.events, ev)
	st.current = State{TypeID: m.typ, Props: m.props}
	st.maybeSnapshot(ev)
	return nil
}

// maybeSnapshot 在类型演变事件后或每隔 SnapshotInterval 个事件落一次快照，
// 使任意重建所需重放的后缀长度有常数上界，与历史演变总次数无关。
func (st *stream) maybeSnapshot(ev Event) {
	if ev.Kind == EvTypeEvolve || (ev.Seq+1)%SnapshotInterval == 0 {
		st.snapshots = append(st.snapshots, snapshot{seq: ev.Seq, state: st.current.clone()})
	}
}

// ImportEvents 批量导入历史事件（迁移场景），不做顺序与合法性校验；
// 顺序问题将在重建时以 ErrAmbiguousOrder 报告。
// 导入是原子替换：实例此前的事件流（若有）被整体替换。
func (s *Store) ImportEvents(instanceID string, events []Event) error {
	if len(events) == 0 {
		return fmt.Errorf("ontology: 导入事件列表为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &stream{orderViolation: firstOrderViolation(events)}
	cp := make([]Event, len(events))
	copy(cp, events)
	for i := range cp {
		cp[i].Seq = i
	}
	st.events = cp
	// 尝试构建快照与当前状态；失败仅意味着禁止后续追加，不影响重建报错。
	m := machine{props: map[string]Value{}}
	ok := true
	for i := range cp {
		if err := m.apply(cp[i], s.rules, false, nil); err != nil {
			ok = false
			break
		}
		st.current = State{TypeID: m.typ, Props: m.props}
		st.maybeSnapshot(cp[i])
	}
	st.broken = !ok
	s.streams[instanceID] = st
	return nil
}

// Events 返回实例事件流的拷贝。
func (s *Store) Events(instanceID string) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.streams[instanceID]
	if !ok {
		return nil, fmt.Errorf("%q: %w", instanceID, ErrInstanceNotFound)
	}
	out := make([]Event, len(st.events))
	copy(out, st.events)
	return out, nil
}

// Rebuild 重建实例在截止时刻 cutoff（含）的状态。
// 重建为只读操作，失败也不会写入任何补偿或修正事件。
func (s *Store) Rebuild(instanceID string, cutoff int64) (State, RebuildStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var stats RebuildStats
	st, ok := s.streams[instanceID]
	if !ok {
		return State{}, stats, fmt.Errorf("%q: %w", instanceID, ErrInstanceNotFound)
	}
	if err := st.check(cutoff); err != nil {
		return State{}, stats, err
	}
	target := st.targetSeq(cutoff)
	stats.EventsTotal = target + 1
	// 命中最近的快照，只重放快照之后的后缀。
	snapIdx := sort.Search(len(st.snapshots), func(i int) bool {
		return st.snapshots[i].seq > target
	}) - 1
	start := State{}
	from := 0
	if snapIdx >= 0 {
		start = st.snapshots[snapIdx].state
		from = st.snapshots[snapIdx].seq + 1
		stats.SnapshotUsed = true
	}
	stats.EventsReplayed = target - from + 1
	state, evolutions, err := replayPrefix(st.events[from:target+1], s.rules, start, nil)
	stats.EvolutionsSeen = evolutions
	if err != nil {
		return State{}, stats, err
	}
	return state.clone(), stats, nil
}

// check 按错误优先级执行重建前检查。
func (st *stream) check(cutoff int64) error {
	// 错误优先级 1：并列记录（仅当违规事件落在截止时刻之前）。
	if st.orderViolation >= 0 && st.events[st.orderViolation].Time <= cutoff {
		return fmt.Errorf("事件序号 %d 与前一事件时刻并列或倒退: %w",
			st.orderViolation, ErrAmbiguousOrder)
	}
	// 错误优先级 2：截止时刻早于首个事件。
	if cutoff < st.events[0].Time {
		return fmt.Errorf("cutoff=%d: %w", cutoff, ErrCutoffBeforeFirstEvent)
	}
	return nil
}

// targetSeq 返回截止时刻（含）之前最后一个事件的下标；调用前须通过 check。
func (st *stream) targetSeq(cutoff int64) int {
	return sort.Search(len(st.events), func(i int) bool {
		return st.events[i].Time > cutoff
	}) - 1
}

// RebuildTrace 与 Rebuild 语义相同，但不走快照、从头完整重放，
// 并返回覆盖每个事件的完整判定记录（审计路径，牺牲性能换取完整证据链）。
func (s *Store) RebuildTrace(instanceID string, cutoff int64) (State, RebuildStats, *DecisionTrace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	trace := &DecisionTrace{InstanceID: instanceID, Cutoff: cutoff}
	var stats RebuildStats
	st, ok := s.streams[instanceID]
	if !ok {
		trace.Err = fmt.Errorf("%q: %w", instanceID, ErrInstanceNotFound)
		return State{}, stats, trace, trace.Err
	}
	if err := st.check(cutoff); err != nil {
		trace.Err = err
		return State{}, stats, trace, err
	}
	target := st.targetSeq(cutoff)
	stats.EventsTotal = target + 1
	stats.EventsReplayed = target + 1
	state, evolutions, err := replayPrefix(st.events[:target+1], s.rules, State{}, &trace.Entries)
	stats.EvolutionsSeen = evolutions
	if err != nil {
		trace.Err = err
		return State{}, stats, trace, err
	}
	trace.Final = state.clone()
	return state.clone(), stats, trace, nil
}
