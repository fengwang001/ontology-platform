package ontology

import "sync"

// EventKind 是判定日志中事件的类别。
type EventKind int

const (
	// EventCommit 一次写入真实生效。
	EventCommit EventKind = iota
	// EventConflict 一次尝试因基线版本落后被判普通冲突。
	EventConflict
	// EventPreempted 一次尝试因被更高权限写入抢占而终止。
	EventPreempted
)

// Event 是判定日志中的一条记录，完整记录判定依据。
type Event struct {
	Seq      uint64
	ActionID string
	ObjectID string
	Priority Priority
	Baseline int64
	Kind     EventKind
	// 以下字段仅在 Kind == EventCommit 时有意义。
	Version  int64
	Mutation Mutation
	Clock    uint64
}

// objectState 是单个对象实例的内部状态。
type objectState struct {
	mu      sync.Mutex
	version int64
	clock   uint64
	props   map[string]any
	wm      priorityWatermark
}

// Store 管理全部对象实例，并维护全局判定日志。
type Store struct {
	mu      sync.Mutex // 保护 objects 与 log
	objects map[string]*objectState
	log     []Event
}

// NewStore 创建一个空 Store。
func NewStore() *Store {
	return &Store{objects: make(map[string]*objectState)}
}

// Create 以给定初始属性创建对象实例（版本从 0 开始）。
func (s *Store) Create(id string, props map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[id] = &objectState{props: cloneProps(props)}
}

func (s *Store) getObject(id string) *objectState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objects[id]
}

// ReadSnapshot 读取实例当前快照，作为一次尝试的判定基线。
func (s *Store) ReadSnapshot(id string) Snapshot {
	st := s.getObject(id)
	if st == nil {
		return Snapshot{Version: -1}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return Snapshot{Version: st.version, Props: cloneProps(st.props)}
}

// TryCommit 尝试在 baseline 基线上提交 mutation。
// 判定顺序：抢占判定（纯查询，不产生任何状态变化）先于版本冲突判定。
// 返回本次尝试结果；若提交成功，同时返回新版本号。
func (s *Store) TryCommit(id, actionID string, p Priority, baseline int64, mut Mutation) (AttemptOutcome, int64) {
	st := s.getObject(id)
	if st == nil {
		return AttemptConflict, 0
	}
	st.mu.Lock()
	// 1. 抢占判定：存在更高权限已生效写入且其版本推进了 baseline。
	//    该判定只读取水位线，不修改版本、属性或时钟。
	if st.wm.preempts(p, baseline) {
		s.appendEvent(Event{
			ActionID: actionID, ObjectID: id, Priority: p,
			Baseline: baseline, Kind: EventPreempted,
		})
		st.mu.Unlock()
		return AttemptPreempted, 0
	}
	// 2. 普通版本冲突判定。
	if st.version != baseline {
		s.appendEvent(Event{
			ActionID: actionID, ObjectID: id, Priority: p,
			Baseline: baseline, Kind: EventConflict,
		})
		st.mu.Unlock()
		return AttemptConflict, 0
	}
	// 3. 提交：推进版本与时钟，应用修改，登记水位线。
	st.version++
	st.clock++
	applyMutation(st.props, mut)
	st.wm.record(p, st.version)
	s.appendEvent(Event{
		ActionID: actionID, ObjectID: id, Priority: p,
		Baseline: baseline, Kind: EventCommit,
		Version: st.version, Mutation: cloneMutation(mut), Clock: st.clock,
	})
	version := st.version
	st.mu.Unlock()
	return AttemptCommitted, version
}

// Events 返回全局判定日志的副本（按 Seq 升序）。
func (s *Store) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.log))
	copy(out, s.log)
	return out
}

// State 返回实例当前 (版本, 时钟, 属性) 的只读视图，用于验证。
func (s *Store) State(id string) (version int64, clock uint64, props map[string]any) {
	st := s.getObject(id)
	if st == nil {
		return -1, 0, nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.version, st.clock, cloneProps(st.props)
}

// WatermarkLevels 返回实例水位线当前追踪的权限等级数量，
// 用于验证抢占判定开销与并发动作总数无关。
func (s *Store) WatermarkLevels(id string) int {
	st := s.getObject(id)
	if st == nil {
		return 0
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.wm.levels()
}

func (s *Store) appendEvent(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev.Seq = uint64(len(s.log))
	s.log = append(s.log, ev)
}

func applyMutation(props map[string]any, mut Mutation) {
	for k, v := range mut.Set {
		props[k] = v
	}
	for _, k := range mut.Del {
		delete(props, k)
	}
}

func cloneProps(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	for k, v := range props {
		out[k] = v
	}
	return out
}

func cloneMutation(mut Mutation) Mutation {
	out := Mutation{Set: cloneProps(mut.Set)}
	if mut.Del != nil {
		out.Del = append([]string(nil), mut.Del...)
	}
	return out
}
