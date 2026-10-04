// Package alias 维护别名成员关系与写索引标记，并原子提交成批动作。
package alias

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/indexreg"
	"ontology/resolve"
)

// 可由 errors.Is 区分的哨兵错误。
var (
	// ErrInvalidArgument 表示动作数越界、名字非法或取值非法。
	ErrInvalidArgument = errors.New("alias: invalid argument")
	// ErrMemberNotFound 表示 mustExist 为真时待移除成员不存在。
	ErrMemberNotFound = errors.New("alias: member not found")
	// ErrMultipleWrite 表示终态中某别名有两个及以上显式写成员。
	ErrMultipleWrite = errors.New("alias: multiple write indices")
	// ErrNoWriteIndex 别名存在但推不出写索引（定义在 resolve 包，此处复用）。
	ErrNoWriteIndex = resolve.ErrNoWriteIndex
)

const (
	kindAdd         = 0
	kindRemove      = 1
	kindRemoveIndex = 2
)

// TriState 表示 isWrite 的三态取值。
type TriState uint8

const (
	// WriteUnspecified 表示未指定写标记。
	WriteUnspecified TriState = 0
	// WriteTrue 表示显式标记为写索引。
	WriteTrue TriState = 1
	// WriteFalse 表示显式标记为非写索引。
	WriteFalse TriState = 2
)

// Member 是别名的一个成员。
type Member struct {
	Alias   string
	Index   string
	IsWrite TriState
	Filter  string
}

// Action 是一次 Update 中的单个动作。
type Action struct {
	kind int
	Member
	mustExist bool
}

// Add 构造“添加/覆盖成员”动作；filter 为空串表示无 filter。
func Add(aliasName, indexName string, isWrite TriState, filter string) Action {
	return Action{kind: kindAdd, Member: Member{
		Alias:   aliasName,
		Index:   indexName,
		IsWrite: isWrite,
		Filter:  filter,
	}}
}

// Remove 构造“移除成员”动作；mustExist 为真时成员不存在即拒绝整批。
func Remove(aliasName, indexName string, mustExist bool) Action {
	return Action{kind: kindRemove, Member: Member{Alias: aliasName, Index: indexName}, mustExist: mustExist}
}

// RemoveIndex 构造“删除索引并移除其全部成员关系”动作。
func RemoveIndex(indexName string) Action {
	return Action{kind: kindRemoveIndex, Member: Member{Index: indexName}}
}

// Manager 组合索引登记表，维护别名成员关系与写索引推定结果。
// 所有方法可并发调用；锁序恒为 manager.mu -> registry.mu。
type Manager struct {
	mu  sync.Mutex
	reg *indexreg.Registry
	// aliases[aliasName][indexName] 为该别名当前成员。
	aliases map[string]map[string]Member
	// writeIndex 为每次提交时一次性推定的写索引；缺键表示无写索引。
	writeIndex map[string]string
}

// NewManager 创建管理器。
func NewManager(reg *indexreg.Registry) *Manager {
	return &Manager{
		reg:        reg,
		aliases:    map[string]map[string]Member{},
		writeIndex: map[string]string{},
	}
}

// Registry 返回底层索引登记表。
func (m *Manager) Registry() *indexreg.Registry { return m.reg }

// ActionError 携带出错动作的下标，供逐步错误定位（带下标）。
// 可用 errors.As 取得本类型并读取 Index，用 errors.Is 判断底层原因。
type ActionError struct {
	Index int
	Err   error
}

func (e *ActionError) Error() string {
	return fmt.Sprintf("alias: action %d: %v", e.Index, e.Err)
}

func (e *ActionError) Unwrap() error { return e.Err }

// Update 原子提交一批 1 到 100 个别名动作（全有或全无）。
// 拒绝次序：参数非法 > 最小下标逐步错误 > 终态名字冲突 > 终态多写索引。
// 终态与提交前完全相同的批次被接受但不推进纪元。
func (m *Manager) Update(actions []Action) error {
	if len(actions) < 1 || len(actions) > 100 {
		return fmt.Errorf("%w: action count %d out of range [1,100]", ErrInvalidArgument, len(actions))
	}
	for i, act := range actions {
		if !validAction(act) {
			return fmt.Errorf("%w: action %d", ErrInvalidArgument, i)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.reg.Lock()
	defer m.reg.Unlock()

	idxWork := m.reg.SnapshotUnlocked()
	aliasWork := m.cloneAliases()

	for i, act := range actions {
		switch act.kind {
		case kindAdd:
			if _, ok := idxWork[act.Index]; !ok {
				return &ActionError{Index: i, Err: indexreg.ErrIndexNotFound}
			}
			group := aliasWork[act.Alias]
			if group == nil {
				group = map[string]Member{}
				aliasWork[act.Alias] = group
			}
			group[act.Index] = Member{
				Alias:   act.Alias,
				Index:   act.Index,
				IsWrite: act.IsWrite,
				Filter:  act.Filter,
			}
		case kindRemove:
			group := aliasWork[act.Alias]
			if _, ok := group[act.Index]; !ok {
				if act.mustExist {
					return &ActionError{Index: i, Err: ErrMemberNotFound}
				}
				continue
			}
			delete(group, act.Index)
			if len(group) == 0 {
				delete(aliasWork, act.Alias)
			}
		case kindRemoveIndex:
			if _, ok := idxWork[act.Index]; !ok {
				return &ActionError{Index: i, Err: indexreg.ErrIndexNotFound}
			}
			delete(idxWork, act.Index)
			for aliasName, group := range aliasWork {
				delete(group, act.Index)
				if len(group) == 0 {
					delete(aliasWork, aliasName)
				}
			}
		default:
			return fmt.Errorf("%w: action %d: unknown kind", ErrInvalidArgument, i)
		}
	}

	if name := firstNameConflict(aliasWork, idxWork); name != "" {
		return fmt.Errorf("%w: %s", indexreg.ErrNameConflict, name)
	}
	if aliasName := firstMultipleWrite(aliasWork); aliasName != "" {
		return fmt.Errorf("%w: %s", ErrMultipleWrite, aliasName)
	}

	writeWork := inferWriteIndices(aliasWork)
	idxChanged := !indexStatesEqual(idxWork, m.reg.SnapshotUnlocked())
	aliasChanged := !aliasesEqual(m.aliases, aliasWork)

	m.reg.ReplaceUnlocked(idxWork)
	m.aliases = aliasWork
	m.writeIndex = writeWork
	if idxChanged || aliasChanged {
		m.reg.BumpEpochUnlocked()
	}
	return nil
}

func validAction(act Action) bool {
	switch act.kind {
	case kindAdd:
		if !indexreg.ValidName(act.Alias) || !indexreg.ValidName(act.Index) {
			return false
		}
		return act.IsWrite <= WriteFalse
	case kindRemove:
		return indexreg.ValidName(act.Alias) && indexreg.ValidName(act.Index)
	case kindRemoveIndex:
		return indexreg.ValidName(act.Index)
	default:
		return false
	}
}

func (m *Manager) cloneAliases() map[string]map[string]Member {
	out := make(map[string]map[string]Member, len(m.aliases))
	for aliasName, group := range m.aliases {
		cp := make(map[string]Member, len(group))
		for idxName, mem := range group {
			cp[idxName] = mem
		}
		out[aliasName] = cp
	}
	return out
}

// firstNameConflict 返回终态中既是索引名又是别名名的字节序最小者。
func firstNameConflict(aliases map[string]map[string]Member, indices map[string]indexreg.State) string {
	var names []string
	for aliasName := range aliases {
		if _, ok := indices[aliasName]; ok {
			names = append(names, aliasName)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
}

// firstMultipleWrite 返回存在两个及以上显式写成员的字节序最小别名。
func firstMultipleWrite(aliases map[string]map[string]Member) string {
	var bad []string
	for aliasName, group := range aliases {
		writes := 0
		for _, mem := range group {
			if mem.IsWrite == WriteTrue {
				writes++
			}
		}
		if writes > 1 {
			bad = append(bad, aliasName)
		}
	}
	if len(bad) == 0 {
		return ""
	}
	sort.Strings(bad)
	return bad[0]
}

// inferWriteIndices 按推定规则一次性计算每个别名的写索引：
// 恰有一个显式真则取它；否则恰有一个成员且未指定则取它；其余无写索引。
func inferWriteIndices(aliases map[string]map[string]Member) map[string]string {
	out := make(map[string]string, len(aliases))
	for aliasName, group := range aliases {
		var trueMember, soleMember string
		trueCount, unspecifiedCount := 0, 0
		for idxName, mem := range group {
			switch mem.IsWrite {
			case WriteTrue:
				trueCount++
				trueMember = idxName
			case WriteUnspecified:
				unspecifiedCount++
			}
			soleMember = idxName
		}
		switch {
		case trueCount == 1:
			out[aliasName] = trueMember
		case len(group) == 1 && unspecifiedCount == 1:
			out[aliasName] = soleMember
		}
	}
	return out
}

func indexStatesEqual(a, b map[string]indexreg.State) bool {
	if len(a) != len(b) {
		return false
	}
	for name, st := range a {
		if b[name] != st {
			return false
		}
	}
	return true
}

func aliasesEqual(a, b map[string]map[string]Member) bool {
	if len(a) != len(b) {
		return false
	}
	for aliasName, ga := range a {
		gb, ok := b[aliasName]
		if !ok || len(ga) != len(gb) {
			return false
		}
		for idxName, ma := range ga {
			if ma != gb[idxName] {
				return false
			}
		}
	}
	return true
}

// Epoch 返回全局纪元（索引与别名共用）。
func (m *Manager) Epoch() uint64 { return m.reg.Epoch() }

// CreateIndex 登记索引；拒绝次序：参数非法 > 与现有索引或别名同名冲突。
func (m *Manager) CreateIndex(name string) error {
	if !indexreg.ValidName(name) {
		return indexreg.ErrInvalidName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reg.Lock()
	defer m.reg.Unlock()
	if _, exists := m.aliases[name]; exists {
		return indexreg.ErrNameConflict
	}
	return m.reg.CreateLocked(name)
}

// CloseIndex 关闭索引；不存在报索引不存在，已关闭为空操作。
func (m *Manager) CloseIndex(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reg.Lock()
	defer m.reg.Unlock()
	return m.reg.CloseLocked(name)
}

// OpenIndex 打开索引；不存在报索引不存在，已打开为空操作。
func (m *Manager) OpenIndex(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reg.Lock()
	defer m.reg.Unlock()
	return m.reg.OpenLocked(name)
}

// Members 返回别名的成员副本（按索引名字节序）。
func (m *Manager) Members(aliasName string) []Member {
	m.mu.Lock()
	defer m.mu.Unlock()
	group := m.aliases[aliasName]
	out := make([]Member, 0, len(group))
	for _, mem := range group {
		out = append(out, mem)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// Aliases 返回所有现存别名（字节序）。
func (m *Manager) Aliases() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.aliases))
	for aliasName := range m.aliases {
		out = append(out, aliasName)
	}
	sort.Strings(out)
	return out
}

// WriteIndex 返回别名推定的写索引（不检查开闭状态）。
func (m *Manager) WriteIndex(aliasName string) (indexName string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name, exists := m.writeIndex[aliasName]
	return name, exists
}

// IndexState 返回索引开闭状态。
func (m *Manager) IndexState(name string) (indexreg.State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reg.Lock()
	defer m.reg.Unlock()
	st, ok := m.reg.StateUnlocked(name)
	return st, ok
}

// View 是 resolve.View 的本包实现，在管理器锁内一次性构造，之后不可变。
type View struct {
	indices    map[string]indexreg.State
	writeIndex map[string]string
	aliases    map[string][]resolve.MemberView
}

// IndexState 实现 resolve.View。
func (v *View) IndexState(name string) (indexreg.State, bool) {
	st, ok := v.indices[name]
	return st, ok
}

// AliasWrite 返回推定写索引；命中（包括无写索引以外的查找）只触碰至多一条成员记录。
func (v *View) AliasWrite(aliasName string, tracer *resolve.Tracer) (string, bool) {
	idxName, ok := v.writeIndex[aliasName]
	if ok {
		tracer.Touch()
	}
	return idxName, ok
}

// AliasMembers 返回读成员视图副本（按索引名字节序）；名字不是现存别名时返回 nil。
func (v *View) AliasMembers(aliasName string) []resolve.MemberView {
	src, ok := v.aliases[aliasName]
	if !ok {
		return nil
	}
	out := make([]resolve.MemberView, len(src))
	copy(out, src)
	return out
}

// SnapshotView 在持锁状态下取当前状态的一致、不可变快照；
// 解析只基于该快照，因而只见某次提交之前或之后的完整状态。
func (m *Manager) SnapshotView() *View {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reg.Lock()
	defer m.reg.Unlock()

	idxSnap := m.reg.SnapshotUnlocked()
	v := &View{
		indices:    make(map[string]indexreg.State, len(idxSnap)),
		writeIndex: make(map[string]string, len(m.writeIndex)),
		aliases:    make(map[string][]resolve.MemberView, len(m.aliases)),
	}
	for name, st := range idxSnap {
		v.indices[name] = st
	}
	for aliasName, idxName := range m.writeIndex {
		v.writeIndex[aliasName] = idxName
	}
	for aliasName, group := range m.aliases {
		members := make([]resolve.MemberView, 0, len(group))
		for _, mem := range group {
			members = append(members, resolve.MemberView{Index: mem.Index, Filter: mem.Filter})
		}
		sort.Slice(members, func(i, j int) bool { return members[i].Index < members[j].Index })
		v.aliases[aliasName] = members
	}
	return v
}
