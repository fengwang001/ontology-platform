// Package acl 提供主体模型与属性级授权存储。
//
// 授权是三元组 (主体, 属性, 读|写)。无任何显式记录即默认拒绝；
// Grant 记为显式允许，Revoke 记为显式拒绝（用于覆盖组授权）。
package acl

// Perm 是对属性的动作：读或写。
type Perm int

const (
	Read Perm = iota
	Write
)

func (p Perm) String() string {
	switch p {
	case Read:
		return "read"
	case Write:
		return "write"
	default:
		return "unknown"
	}
}

// Subject 是授权主体：用户或组。
type Subject struct {
	ID    string
	Group bool
}

// User 构造用户主体。
func User(id string) Subject { return Subject{ID: id} }

// GroupSubject 构造组主体。
func GroupSubject(id string) Subject { return Subject{ID: id, Group: true} }

// entry 是单条授权的显式结论。
type entry int

const (
	entryUnset entry = iota
	entryAllow
	entryDeny
)

type grantKey struct {
	subject Subject
	attr    string
	perm    Perm
}

// Store 保存组成员关系与授权记录。零值不可用，用 NewStore 构造。
type Store struct {
	// members: 组 ID -> 该组的直接成员（用户或子组）。
	members map[string]map[Subject]bool
	// parents: 成员 -> 直接包含它的组 ID 集合（反向索引）。
	parents map[Subject]map[string]bool
	grants  map[grantKey]entry

	// mutations 是授权/成员关系的非导出变更计数，供上层判定缓存是否需要失效。
	mutations int
}

// NewStore 创建空授权存储。
func NewStore() *Store {
	return &Store{
		members: map[string]map[Subject]bool{},
		parents: map[Subject]map[string]bool{},
		grants:  map[grantKey]entry{},
	}
}

// AddMember 把 m 加入组 groupID（m 可以是用户或嵌套组）。
func (s *Store) AddMember(groupID string, m Subject) {
	if s.members[groupID] == nil {
		s.members[groupID] = map[Subject]bool{}
	}
	if s.members[groupID][m] {
		return
	}
	s.members[groupID][m] = true
	if s.parents[m] == nil {
		s.parents[m] = map[string]bool{}
	}
	s.parents[m][groupID] = true
	s.mutations++
}

// RemoveMember 把 m 从组 groupID 移除。
func (s *Store) RemoveMember(groupID string, m Subject) {
	if !s.members[groupID][m] {
		return
	}
	delete(s.members[groupID], m)
	delete(s.parents[m], groupID)
	if len(s.parents[m]) == 0 {
		delete(s.parents, m)
	}
	s.mutations++
}

// Grant 显式授予 subj 对 attr 的 perm 权限。
func (s *Store) Grant(subj Subject, attr string, perm Perm) {
	s.setEntry(subj, attr, perm, entryAllow)
}

// Revoke 显式拒绝 subj 对 attr 的 perm 权限；用于覆盖组级授权。
func (s *Store) Revoke(subj Subject, attr string, perm Perm) {
	s.setEntry(subj, attr, perm, entryDeny)
}

func (s *Store) setEntry(subj Subject, attr string, perm Perm, e entry) {
	if s.grants[grantKey{subj, attr, perm}] == e {
		return
	}
	s.grants[grantKey{subj, attr, perm}] = e
	s.mutations++
}

// DirectEntry 返回 subj 自身对 (attr, perm) 的显式条目。
// set 为 false 表示没有直接记录；allow 仅在 set 为 true 时有意义。
func (s *Store) DirectEntry(subj Subject, attr string, perm Perm) (allow, set bool) {
	switch s.grants[grantKey{subj, attr, perm}] {
	case entryAllow:
		return true, true
	case entryDeny:
		return false, true
	default:
		return false, false
	}
}

// GroupEntry 返回组 groupID 对 (attr, perm) 的显式条目。
func (s *Store) GroupEntry(groupID, attr string, perm Perm) (allow, set bool) {
	return s.DirectEntry(GroupSubject(groupID), attr, perm)
}

// ParentGroups 返回直接包含 m 的组 ID。
func (s *Store) ParentGroups(m Subject) []string {
	ids := make([]string, 0, len(s.parents[m]))
	for id := range s.parents[m] {
		ids = append(ids, id)
	}
	return ids
}

// Mutations 返回非导出的存储变更计数。
func (s *Store) Mutations() int { return s.mutations }
