package ontology

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store 是对象实例存储，提供乐观更新与独占占用两种并发控制。
type Store struct {
	mu        sync.Mutex
	clock     Clock
	leaseTTL  time.Duration
	types     map[string]ObjectType
	instances map[InstanceID]*instanceState
	links     []Link
	audit     *AuditLog
	// heldMax 记录每个动作当前持有的最大实例 ID（顺序规则判定用，O(1)）。
	heldMax map[ActionID]InstanceID
	heldSet map[ActionID]map[InstanceID]bool
}

type occupancyRecord struct {
	holder  ActionID
	token   uint64
	expires time.Time
}

type instanceState struct {
	typ       string
	version   uint64
	props     map[string]PropertyValue
	occ       *occupancyRecord // nil 表示未被占用
	lastToken uint64           // 单调递增的栅栏令牌来源
}

// Option 配置 Store。
type Option func(*Store)

// WithClock 注入时钟（测试用假时钟）。
func WithClock(c Clock) Option { return func(s *Store) { s.clock = c } }

// WithLeaseTTL 设置占用租约时长。
func WithLeaseTTL(d time.Duration) Option { return func(s *Store) { s.leaseTTL = d } }

// NewStore 创建存储。
func NewStore(opts ...Option) *Store {
	s := &Store{
		clock:     RealClock{},
		leaseTTL:  5 * time.Second,
		types:     map[string]ObjectType{},
		instances: map[InstanceID]*instanceState{},
		audit:     newAuditLog(),
		heldMax:   map[ActionID]InstanceID{},
		heldSet:   map[ActionID]map[InstanceID]bool{},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Audit 返回审计日志。
func (s *Store) Audit() *AuditLog { return s.audit }

// expired 是判定占用是否失效的唯一依据：当前时间已到达或超过租约截止时间。
// 调用方必须持有 s.mu。
func (s *Store) expired(occ *occupancyRecord) bool {
	return !s.clock.Now().Before(occ.expires)
}

// liveOcc 返回当前生效的占用记录；租约过期视为不存在（占用可被重新获得）。
// 调用方必须持有 s.mu。
func (s *Store) liveOcc(st *instanceState) *occupancyRecord {
	if st.occ != nil && s.expired(st.occ) {
		return nil
	}
	return st.occ
}

// validateLocked 校验一组变更是否满足对象类型的基数/必填约束。
// 调用方必须持有 s.mu。
func (s *Store) validateLocked(st *instanceState, muts []Mutation) error {
	t, ok := s.types[st.typ]
	if !ok {
		return nil
	}
	merged := map[string]PropertyValue{}
	for k, v := range st.props {
		merged[k] = v
	}
	for _, m := range muts {
		merged[m.Property] = m.Value
	}
	for _, spec := range t.Properties {
		v, present := merged[spec.Name]
		if spec.Required && (!present || v == nil) {
			return reject(RejectCardinality,
				fmt.Sprintf("property %q is required", spec.Name))
		}
		if !present || !spec.IsList {
			continue
		}
		list, ok := v.([]any)
		if !ok {
			return reject(RejectCardinality,
				fmt.Sprintf("property %q must be a list", spec.Name))
		}
		if len(list) < spec.Min || (spec.Max >= 0 && len(list) > spec.Max) {
			return reject(RejectCardinality,
				fmt.Sprintf("property %q cardinality %d out of [%d,%d]", spec.Name, len(list), spec.Min, spec.Max))
		}
	}
	return nil
}

func formatMutations(muts []Mutation) string {
	parts := make([]string, 0, len(muts))
	for _, m := range muts {
		parts = append(parts, fmt.Sprintf("%s=%v", m.Property, m.Value))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// stage 暂存占用期间的变更。
func (s *Store) stage(o *Occupancy, muts []Mutation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := AuditRecord{
		At:        s.clock.Now(),
		Kind:      OpStage,
		Instance:  o.instance,
		Actor:     string(o.holder),
		Token:     o.token,
		Mutations: append([]Mutation(nil), muts...),
		Input:     fmt.Sprintf("token=%d mutations=%s", o.token, formatMutations(muts)),
	}
	if err := s.checkHolderLocked(o); err != nil {
		rec.Basis = err.Error()
		if re, ok := err.(*RejectError); ok {
			rec.Code = re.Code
		}
		rec.NewVersion = s.instances[o.instance].version
		s.audit.append(rec)
		return err
	}
	st := s.instances[o.instance]
	if err := s.validateLocked(st, muts); err != nil {
		rec.Basis = "constraint validation failed: " + err.Error()
		if re, ok := err.(*RejectError); ok {
			rec.Code = re.Code
		}
		rec.NewVersion = st.version
		s.audit.append(rec)
		return err
	}
	o.staged = append(o.staged, muts...)
	rec.Basis = "staged"
	rec.Code = RejectNone
	rec.NewVersion = st.version
	s.audit.append(rec)
	return nil
}

// checkHolderLocked 校验占用句柄仍然有效：未完成、未过期、栅栏令牌匹配。
// 调用方必须持有 s.mu。
func (s *Store) checkHolderLocked(o *Occupancy) error {
	if o.done {
		return reject(RejectOccupancyLost, "occupancy already finished")
	}
	st := s.instances[o.instance]
	occ := st.occ
	if occ == nil || occ.holder != o.holder || occ.token != o.token {
		return reject(RejectOccupancyLost,
			fmt.Sprintf("occupancy on %s lost (fencing token mismatch)", o.instance))
	}
	if s.expired(occ) {
		return reject(RejectOccupancyLost,
			fmt.Sprintf("occupancy lease on %s expired", o.instance))
	}
	return nil
}

// heartbeat 续期租约。
func (s *Store) heartbeat(o *Occupancy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.checkHolderLocked(o)
	rec := AuditRecord{
		At:       s.clock.Now(),
		Kind:     OpHeartbeat,
		Instance: o.instance,
		Actor:    string(o.holder),
		Token:    o.token,
		Input:    fmt.Sprintf("token=%d", o.token),
	}
	if err != nil {
		rec.Basis = err.Error()
		rec.Code = RejectOccupancyLost
		rec.NewVersion = s.instances[o.instance].version
		s.audit.append(rec)
		return err
	}
	st := s.instances[o.instance]
	st.occ.expires = s.clock.Now().Add(s.leaseTTL)
	rec.Basis = fmt.Sprintf("lease extended to %s", st.occ.expires.Format(time.RFC3339Nano))
	rec.Code = RejectNone
	rec.NewVersion = st.version
	s.audit.append(rec)
	return nil
}

// commit 原子提交暂存变更并释放占用。
func (s *Store) commit(o *Occupancy) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := AuditRecord{
		At:        s.clock.Now(),
		Kind:      OpCommit,
		Instance:  o.instance,
		Actor:     string(o.holder),
		Token:     o.token,
		Mutations: append([]Mutation(nil), o.staged...),
		Input:     fmt.Sprintf("token=%d staged=%s", o.token, formatMutations(o.staged)),
	}
	if err := s.checkHolderLocked(o); err != nil {
		rec.Basis = err.Error()
		rec.Code = RejectOccupancyLost
		rec.NewVersion = s.instances[o.instance].version
		s.audit.append(rec)
		return 0, err
	}
	st := s.instances[o.instance]
	for _, m := range o.staged {
		st.props[m.Property] = m.Value
	}
	st.version++
	s.releaseLocked(o.instance, st, o.holder)
	o.done = true
	rec.Basis = fmt.Sprintf("version %d -> %d, occupancy released", st.version-1, st.version)
	rec.Code = RejectNone
	rec.NewVersion = st.version
	s.audit.append(rec)
	return st.version, nil
}

// abort 丢弃暂存变更并释放占用。
func (s *Store) abort(o *Occupancy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.instances[o.instance]
	rec := AuditRecord{
		At:       s.clock.Now(),
		Kind:     OpAbort,
		Instance: o.instance,
		Actor:    string(o.holder),
		Token:    o.token,
		Input:    fmt.Sprintf("token=%d", o.token),
	}
	// 中止只允许由当前持有方执行；令牌不匹配说明占用已被接管，无需再释放。
	if st.occ != nil && st.occ.holder == o.holder && st.occ.token == o.token {
		s.releaseLocked(o.instance, st, o.holder)
	}
	o.done = true
	rec.Basis = "staged changes discarded, occupancy released"
	rec.Code = RejectNone
	rec.NewVersion = st.version
	s.audit.append(rec)
	return nil
}

// releaseLocked 释放实例上的占用并更新动作持有索引。
// 调用方必须持有 s.mu。
func (s *Store) releaseLocked(id InstanceID, st *instanceState, holder ActionID) {
	st.occ = nil
	set := s.heldSet[holder]
	delete(set, id)
	if len(set) == 0 {
		delete(s.heldSet, holder)
		delete(s.heldMax, holder)
		return
	}
	s.heldMax[holder] = maxID(set)
}

// maxID 在单个动作自身持有的集合上求最大值，开销只与该动作持有的实例数有关，
// 与系统实例总数、占用记录总数无关。
func maxID(set map[InstanceID]bool) InstanceID {
	ids := make([]InstanceID, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids[len(ids)-1]
}
