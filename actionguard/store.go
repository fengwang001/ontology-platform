package actionguard

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// object 是内部持久化状态：属性、撤销标记、版本号、历史序列。
type object struct {
	id      string
	attrs   map[string]string
	revoked bool
	version int64
	history []HistoryRecord
}

// HistoryRecord 是对象（或动作类型）历史序列中的一条记录。
// 只有被接受的调用才会进入历史；前置拒绝与后置放弃都不会留下痕迹。
type HistoryRecord struct {
	CallID     string
	ActionType string
	Version    int64
}

// Store 是线程安全的持久化状态容器。
type Store struct {
	mu       sync.Mutex
	objects  map[string]*object
	links    map[string]bool
	accepted []HistoryRecord // 被接受调用的全局序列（全序）
	audit    []AuditEntry
	fails    []AuditEntry // 独立的后置失败轨迹
}

func NewStore() *Store {
	return &Store{objects: map[string]*object{}, links: map[string]bool{}}
}

func (s *Store) CreateObject(id string, attrs map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := map[string]string{}
	for k, v := range attrs {
		cp[k] = v
	}
	s.objects[id] = &object{id: id, attrs: cp, version: 0}
}

func (s *Store) SetLink(key string, present bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links[key] = present
}

// Revoke 供并发测试使用：外部并发撤销目标对象。
func (s *Store) Revoke(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o := s.objects[id]; o != nil {
		o.revoked = true
	}
}

// Snapshot 返回执行前已持久化状态的深拷贝快照。
// 快照与 Store 之后的任何变化完全隔离。
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Lock / Unlock 仅供执行器在一次调用内把
// “取快照 → 前置 → 计划 → 后置 → 提交”收敛为单个临界区，
// 从而使并发调用的被接受集合与效果严格等价于某个全序串行执行。
func (s *Store) Lock()   { s.mu.Lock() }
func (s *Store) Unlock() { s.mu.Unlock() }

func (s *Store) snapshotForExecutor() *immutableSnapshot { return s.snapshotLocked() }

func (s *Store) projectLocked(base *immutableSnapshot, plan *Plan) *immutableSnapshot {
	return s.project(base, plan)
}

func (s *Store) commitLocked(callID, actionType string, plan *Plan) {
	s.commitPlan(callID, actionType, plan)
}

func (s *Store) appendAuditLocked(e AuditEntry)   { s.audit = append(s.audit, e) }
func (s *Store) appendFailureLocked(e AuditEntry) { s.fails = append(s.fails, e) }

func (s *Store) snapshotLocked() *immutableSnapshot {
	objs := make(map[string]*object, len(s.objects))
	for id, o := range s.objects {
		attrs := make(map[string]string, len(o.attrs))
		for k, v := range o.attrs {
			attrs[k] = v
		}
		h := make([]HistoryRecord, len(o.history))
		copy(h, o.history)
		objs[id] = &object{id: id, attrs: attrs, revoked: o.revoked, version: o.version, history: h}
	}
	links := make(map[string]bool, len(s.links))
	for k, v := range s.links {
		links[k] = v
	}
	return &immutableSnapshot{objects: objs, links: links}
}

// project 返回“执行前快照 + 最终写入计划”的投影快照。
// 投影从深拷贝出发并叠加计划的最终值；中间状态从未进入 Plan，
// 因此这里观察到的只能是本次调用计划写入的最终结果。
func (s *Store) project(base *immutableSnapshot, plan *Plan) *immutableSnapshot {
	proj := &immutableSnapshot{
		objects: make(map[string]*object, len(base.objects)),
		links:   map[string]bool{},
	}
	for id, o := range base.objects {
		attrs := make(map[string]string, len(o.attrs))
		for k, v := range o.attrs {
			attrs[k] = v
		}
		proj.objects[id] = &object{id: id, attrs: attrs, revoked: o.revoked, version: o.version}
	}
	for k, v := range base.links {
		proj.links[k] = v
	}
	for id, finals := range plan.ObjectAttrs {
		o := proj.objects[id]
		if o == nil {
			o = &object{id: id, attrs: map[string]string{}}
			proj.objects[id] = o
		}
		for attr, val := range finals {
			o.attrs[attr] = val
		}
	}
	for id, revoked := range plan.ObjectRevoked {
		if o := proj.objects[id]; o != nil {
			o.revoked = revoked
		}
	}
	for key, present := range plan.Links {
		proj.links[key] = present
	}
	return proj
}

// Commit 在执行器已完成全部校验后原子提交最终计划。
// 版本号只在此时分配；此前的任何拒绝/放弃都不会消耗版本号。
func (s *Store) Commit(callID, actionType string, plan *Plan, targets []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitPlan(callID, actionType, plan)
}

func (s *Store) commitPlan(callID, actionType string, plan *Plan) {
	touched := map[string]bool{}
	for id, finals := range plan.ObjectAttrs {
		o := s.objects[id]
		if o == nil {
			o = &object{id: id, attrs: map[string]string{}}
			s.objects[id] = o
		}
		for attr, val := range finals {
			o.attrs[attr] = val
		}
		touched[id] = true
	}
	for id, revoked := range plan.ObjectRevoked {
		if o := s.objects[id]; o != nil {
			o.revoked = revoked
			touched[id] = true
		}
	}
	for key, present := range plan.Links {
		s.links[key] = present
	}
	// 稳定的全序：按对象 ID 排序后分配版本号，结果与调用内步骤顺序无关。
	ids := make([]string, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := s.objects[id]
		o.version++
		o.history = append(o.history, HistoryRecord{CallID: callID, ActionType: actionType, Version: o.version})
	}
	rec := HistoryRecord{CallID: callID, ActionType: actionType}
	s.accepted = append(s.accepted, rec)
}

func (s *Store) appendAudit(e AuditEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, e)
}

func (s *Store) appendFailure(e AuditEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails = append(s.fails, e)
}

func (s *Store) AuditTrail() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEntry, len(s.audit))
	copy(out, s.audit)
	return out
}

// FailureTrail 是独立于对象状态的后置失败审计轨迹。
func (s *Store) FailureTrail() []AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEntry, len(s.fails))
	copy(out, s.fails)
	return out
}

func (s *Store) AcceptedCalls() []HistoryRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]HistoryRecord, len(s.accepted))
	copy(out, s.accepted)
	return out
}

func (s *Store) ObjectHistory(id string) []HistoryRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.objects[id]
	if o == nil {
		return nil
	}
	out := make([]HistoryRecord, len(o.history))
	copy(out, o.history)
	return out
}

// ObjectState 返回对象属性与版本的快照副本，供测试核对状态不变性。
func (s *Store) ObjectState(id string) (attrs map[string]string, version int64, revoked bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.objects[id]
	if o == nil {
		return nil, 0, false, false
	}
	cp := map[string]string{}
	for k, v := range o.attrs {
		cp[k] = v
	}
	return cp, o.version, o.revoked, true
}

// immutableSnapshot 是 Snapshot 的深拷贝实现。
type immutableSnapshot struct {
	objects map[string]*object
	links   map[string]bool
}

func (sn *immutableSnapshot) Version(objID string) int64 {
	if o := sn.objects[objID]; o != nil {
		return o.version
	}
	return 0
}

func (sn *immutableSnapshot) Revoked(objID string) bool {
	if o := sn.objects[objID]; o != nil {
		return o.revoked
	}
	return false
}

func (sn *immutableSnapshot) Attr(objID, attr string) (string, bool) {
	o := sn.objects[objID]
	if o == nil {
		return "", false
	}
	v, ok := o.attrs[attr]
	return v, ok
}

// allIDsJoined 返回快照中全部对象 ID 的稳定排序拼接，
// 供后置不变量显式引用“全集”。投影快照也可使用，结果为最终全集。
func (sn *immutableSnapshot) allIDsJoined() string {
	ids := make([]string, 0, len(sn.objects))
	for id := range sn.objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// Atom 求值一个已实例化的原子。
// 原子字符串形如 kind\x1farg\x1farg（\x1f 单元分隔符避免与业务值冲突）。
func (sn *immutableSnapshot) Atom(grounded string) bool {
	parts := strings.Split(grounded, "\x1f")
	kind := parts[0]
	args := parts[1:]
	switch kind {
	case "obj_exists":
		o := sn.objects[args[0]]
		return o != nil && !o.revoked
	case "revoked":
		o := sn.objects[args[0]]
		return o != nil && o.revoked
	case "attr_eq":
		o := sn.objects[args[0]]
		return o != nil && o.attrs[args[1]] == args[2]
	case "attr_gt":
		return sn.numAttr(args[0], args[1]) > num(args[2])
	case "attr_gte":
		return sn.numAttr(args[0], args[1]) >= num(args[2])
	case "link":
		return sn.links[strings.Join(args, "\x1f")]
	case "sum_attr_eq":
		// 多对象集合（args[0] 中以逗号分隔的全部对象 ID，可为输入变量
		// 显式给出的全集）的属性之和等于常量，用于表达“总量守恒”等
		// 跨越本次写入全部对象的后置不变量。后置校验对同一投影快照
		// 一次性求值该全集，不存在先后快照差异。
		ids := strings.Split(args[0], ",")
		return sumAttr(sn, ids, args[1]) == num(args[2])
	default:
		return false
	}
}

func (sn *immutableSnapshot) numAttr(id, attr string) int64 {
	o := sn.objects[id]
	if o == nil {
		return 0
	}
	return num(o.attrs[attr])
}

func sumAttr(sn *immutableSnapshot, ids []string, attr string) int64 {
	var sum int64
	for _, id := range ids {
		sum += sn.numAttr(id, attr)
	}
	return sum
}

func num(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

// formatAtom 将模板与环境绑定为原子字符串。
func formatAtom(spec AtomSpec, env map[string]string) string {
	parts := make([]string, 0, len(spec.Args)+1)
	parts = append(parts, spec.Kind)
	for _, a := range spec.Args {
		switch v := a.(type) {
		case ConstArg:
			parts = append(parts, string(v))
		case VarArg:
			parts = append(parts, env[string(v)])
		}
	}
	return strings.Join(parts, "\x1f")
}

func atomStringForAudit(grounded string) string {
	return fmt.Sprintf("%q", grounded)
}
