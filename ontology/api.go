package ontology

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// RegisterType 注册对象类型。
func (s *Store) RegisterType(t ObjectType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.types[t.Name] = t
}

// AddLink 登记实例间链接。
func (s *Store) AddLink(l Link) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links = append(s.links, l)
}

// CreateInstance 创建实例，初始版本为 1。
func (s *Store) CreateInstance(id InstanceID, typ string, props map[string]PropertyValue) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.types[typ]; !ok {
		return fmt.Errorf("unknown object type %q", typ)
	}
	if _, ok := s.instances[id]; ok {
		return fmt.Errorf("instance %q already exists", id)
	}
	cp := map[string]PropertyValue{}
	for k, v := range props {
		cp[k] = v
	}
	s.instances[id] = &instanceState{typ: typ, version: 1, props: cp}
	return nil
}

// Get 读取实例的一致性快照：要么看到占用开始之前、要么看到占用结束之后的状态。
// 占用期间动作暂存的变更对外不可见。
func (s *Store) Get(id InstanceID) (InstanceSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.instances[id]
	if !ok {
		return InstanceSnapshot{}, fmt.Errorf("instance %q not found", id)
	}
	snap := InstanceSnapshot{
		ID:         id,
		Type:       st.typ,
		Version:    st.version,
		Properties: map[string]PropertyValue{},
	}
	for k, v := range st.props {
		snap.Properties[k] = v
	}
	if st.occ != nil && !s.expired(st.occ) {
		snap.Occupied = true
		snap.Holder = st.occ.holder
	}
	return snap, nil
}

// Update 普通乐观更新：依据期望版本判定冲突。
// 判定顺序固定：先占用冲突（RejectInstanceOccupied），再基数约束，最后版本落后
// （RejectVersionStale）。任何拒绝都不会改变实例版本号。
func (s *Store) Update(caller CallerID, id InstanceID, expectedVersion uint64, muts ...Mutation) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.instances[id]
	if !ok {
		return 0, fmt.Errorf("instance %q not found", id)
	}
	rec := AuditRecord{
		At:              s.clock.Now(),
		Kind:            OpUpdate,
		Instance:        id,
		Actor:           string(caller),
		ExpectedVersion: expectedVersion,
		Mutations:       append([]Mutation(nil), muts...),
		Input:           fmt.Sprintf("expectedVersion=%d mutations=%s", expectedVersion, formatMutations(muts)),
	}
	// 1. 占用冲突判定先于版本判定。
	if st.occ != nil && !s.expired(st.occ) {
		rec.Basis = fmt.Sprintf("occupied by action=%s token=%d", st.occ.holder, st.occ.token)
		rec.Code = RejectInstanceOccupied
		rec.NewVersion = st.version
		s.audit.append(rec)
		return st.version, reject(RejectInstanceOccupied,
			fmt.Sprintf("instance %s is occupied by action %s", id, st.occ.holder))
	}
	// 2. 基数/约束校验（不消耗版本）。
	if err := s.validateLocked(st, muts); err != nil {
		var re *RejectError
		if errors.As(err, &re) {
			rec.Code = re.Code
		}
		rec.Basis = "constraint validation failed: " + err.Error()
		rec.NewVersion = st.version
		s.audit.append(rec)
		return st.version, err
	}
	// 3. 乐观版本判定。
	if expectedVersion != st.version {
		rec.Basis = fmt.Sprintf("expectedVersion=%d currentVersion=%d", expectedVersion, st.version)
		rec.Code = RejectVersionStale
		rec.NewVersion = st.version
		s.audit.append(rec)
		return st.version, reject(RejectVersionStale,
			fmt.Sprintf("instance %s version stale: expected %d, current %d", id, expectedVersion, st.version))
	}
	for _, m := range muts {
		st.props[m.Property] = m.Value
	}
	st.version++
	rec.Basis = fmt.Sprintf("version %d -> %d", st.version-1, st.version)
	rec.Code = RejectNone
	rec.NewVersion = st.version
	s.audit.append(rec)
	return st.version, nil
}

// Acquire 申请实例上的独占占用权；非阻塞，失败立即返回拒绝，系统不做排队或自动重试。
//
// 判定顺序固定：
//  1. 顺序规则：动作当前已持有比 id 更大的实例时，拒绝（RejectOrderConflict），
//     这是跨实例占用的确定性死锁规避规则——所有动作必须按实例 ID 升序申请，
//     谁违反顺序谁被拒绝，结果可预先推导；
//  2. 占用冲突：实例被另一动作的未过期租约占用时，拒绝（RejectOccupiedByAction）；
//  3. 租约已过期的占用视为失效，由新持有方接管并颁发更大的栅栏令牌，
//     旧持有方此后的任何操作都会因令牌不匹配而被拒绝（RejectOccupancyLost）。
func (s *Store) Acquire(action ActionID, id InstanceID) (*Occupancy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.instances[id]
	if !ok {
		return nil, fmt.Errorf("instance %q not found", id)
	}
	rec := AuditRecord{
		At:       s.clock.Now(),
		Kind:     OpAcquire,
		Instance: id,
		Actor:    string(action),
		Input:    fmt.Sprintf("action=%s target=%s", action, id),
	}
	// 1. 确定性顺序规则：只允许按实例 ID 升序持有。
	if max, held := s.heldMax[action]; held && max > id {
		rec.Basis = fmt.Sprintf("action holds %s > target %s (ascending order required)", max, id)
		rec.Code = RejectOrderConflict
		rec.NewVersion = st.version
		s.audit.append(rec)
		return nil, reject(RejectOrderConflict,
			fmt.Sprintf("action %s holds %s, cannot acquire lower-ordered %s", action, max, id))
	}
	// 2. 占用冲突：未过期租约立即拒绝，不阻塞、不排队。
	if occ := s.liveOcc(st); occ != nil {
		rec.Basis = fmt.Sprintf("occupied by action=%s token=%d expires=%s",
			occ.holder, occ.token, occ.expires.Format(time.RFC3339Nano))
		rec.Code = RejectOccupiedByAction
		rec.NewVersion = st.version
		s.audit.append(rec)
		return nil, reject(RejectOccupiedByAction,
			fmt.Sprintf("instance %s is occupied by action %s", id, occ.holder))
	}
	// 3. 获得占用（含对失效租约的接管），颁发单调递增栅栏令牌。
	st.lastToken++
	st.occ = &occupancyRecord{
		holder:  action,
		token:   st.lastToken,
		expires: s.clock.Now().Add(s.leaseTTL),
	}
	set := s.heldSet[action]
	if set == nil {
		set = map[InstanceID]bool{}
		s.heldSet[action] = set
	}
	set[id] = true
	if cur, ok := s.heldMax[action]; !ok || id > cur {
		s.heldMax[action] = id
	}
	rec.Basis = fmt.Sprintf("granted token=%d expires=%s", st.occ.token,
		st.occ.expires.Format(time.RFC3339Nano))
	rec.Code = RejectNone
	rec.Token = st.occ.token
	rec.NewVersion = st.version
	s.audit.append(rec)
	return &Occupancy{
		store:    s,
		instance: id,
		holder:   action,
		token:    st.occ.token,
	}, nil
}

// Linked 返回与给定实例通过链接直接关联的实例集合（升序、去重）。
func (s *Store) Linked(id InstanceID) []InstanceID {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := map[InstanceID]bool{}
	for _, l := range s.links {
		if l.From == id {
			set[l.To] = true
		}
		if l.To == id {
			set[l.From] = true
		}
	}
	out := make([]InstanceID, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
