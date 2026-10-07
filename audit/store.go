package audit

import (
	"fmt"
	"sort"
	"sync"
)

// StateStore 按对象类型存放各实例的当前值，并以事务方式提供
// “暂存 → 提交”两段式写入：只有审计记录写入成功后才允许 Commit，
// 审计写入失败时调用 Rollback，底层状态保持原样，从存储层面
// 保证“状态与审计同生同灭”。
type StateStore struct {
	mu sync.Mutex
	// states[typeName][instance] = value
	states map[string]map[string]string

	// 访问计数用于可验证地证明区间重放开销与历史总长无关。
	reads int
}

// NewStateStore 创建空的状态存储。
func NewStateStore() *StateStore {
	return &StateStore{states: make(map[string]map[string]string)}
}

// CreateInstance 注册一个实例并赋初值；重复创建属于参数非法。
func (s *StateStore) CreateInstance(typeName, instance, initialValue string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	objs := s.states[typeName]
	if objs == nil {
		objs = make(map[string]string)
		s.states[typeName] = objs
	}
	if _, ok := objs[instance]; ok {
		return &IllegalRequestError{Reason: fmt.Sprintf("instance %q already exists", instance)}
	}
	objs[instance] = initialValue
	return nil
}

// Snapshot 返回某类型当前状态的一份拷贝（供快照/检查使用）。
func (s *StateStore) Snapshot(typeName string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copyLocked(typeName)
}

func (s *StateStore) copyLocked(typeName string) map[string]string {
	out := make(map[string]string, len(s.states[typeName]))
	for k, v := range s.states[typeName] {
		out[k] = v
	}
	return out
}

// Get 读取单个实例当前值，ok=false 表示实例不存在。
func (s *StateStore) Get(typeName, instance string) (value string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	v, ok := s.states[typeName][instance]
	return v, ok
}

// Txn 是基于“写入前状态拷贝”的事务。暂存阶段不触碰 StateStore，
// 因此无论最终 Commit 还是 Rollback，提交点之前底层状态都不改变。
type Txn struct {
	store    *StateStore
	typeName string
	// working 是本次事务开始时该类型完整状态的私有拷贝。
	working map[string]string
	// baseline 保留 Begin 时刻的状态，用于计算变更前值。
	baseline map[string]string
	written  map[string]struct{}
}

// Begin 开启一次事务（快照隔离，互不影响底层状态）。
func (s *StateStore) Begin(typeName string) *Txn {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	working := s.copyLocked(typeName)
	return &Txn{
		store:    s,
		typeName: typeName,
		working:  working,
		baseline: s.copyLocked(typeName),
		written:  make(map[string]struct{}),
	}
}

// Exists 判断实例是否存在（基于事务私有视图）。
func (t *Txn) Exists(instance string) bool {
	_, ok := t.working[instance]
	return ok
}

// Read 从事务私有视图读取。
func (t *Txn) Read(instance string) (string, bool) {
	v, ok := t.working[instance]
	return v, ok
}

// Write 把写入意图暂存在私有视图中，不触碰底层状态。
func (t *Txn) Write(instance, value string) {
	t.working[instance] = value
	t.written[instance] = struct{}{}
}

// WorkingView 返回私有视图拷贝（供订正记录计算变更前值等）。
func (t *Txn) WorkingView() map[string]string {
	out := make(map[string]string, len(t.working))
	for k, v := range t.working {
		out[k] = v
	}
	return out
}

// WrittenInstances 返回本次事务实际写入过的实例（确定性排序）。
func (t *Txn) WrittenInstances() []string {
	out := make([]string, 0, len(t.written))
	for inst := range t.written {
		out = append(out, inst)
	}
	sort.Strings(out)
	return out
}

// Baseline 返回某实例在事务开始时的值。
func (t *Txn) Baseline(instance string) (string, bool) {
	v, ok := t.baseline[instance]
	return v, ok
}

// Commit 在审计记录写入成功之后调用，把私有视图整体替换为该类型
// 的当前状态。与审计日志 Append 在同一把类型锁保护下完成，
// 因而“审计记录 + 状态变更”对外部而言是一个不可分割的动作。
func (t *Txn) Commit() {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	t.store.states[t.typeName] = t.WorkingView()
}

// Rollback 丢弃私有视图。底层状态自 Begin 起从未被修改，
// 因此这里无需任何反向操作，天然保证回退彻底。
func (t *Txn) Rollback() {
	t.working = nil
	t.written = nil
	t.baseline = nil
}

// ResetCounters / ReadCount 用于重放开销的可验证证明。
func (s *StateStore) ResetCounters() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = 0
}

func (s *StateStore) ReadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}
