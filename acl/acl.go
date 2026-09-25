// Package acl 定义主体模型（用户/组）与属性级读写授权。
//
// 授权是三元组 (主体, 属性, 读|写)，可授予也可撤销；组授权对其成员
// （含嵌套组的成员）生效。默认策略为默认拒绝：无显式授权即无权限。
package acl

// Action 是对属性的动作类别。
type Action int

const (
	Read Action = iota
	Write
)

func (a Action) String() string {
	if a == Write {
		return "write"
	}
	return "read"
}

// SubjectKind 区分用户与组。
type SubjectKind int

const (
	UserKind SubjectKind = iota
	GroupKind
)

// Subject 是授权主体：用户或组。
type Subject struct {
	Kind SubjectKind
	ID   string
}

// User 返回用户主体。
func User(id string) Subject { return Subject{Kind: UserKind, ID: id} }

// Group 返回组主体。
func Group(id string) Subject { return Subject{Kind: GroupKind, ID: id} }

func (s Subject) String() string {
	if s.Kind == GroupKind {
		return "group:" + s.ID
	}
	return "user:" + s.ID
}

// grantSet 记录某主体被授予的 (属性, 动作) 集合。
type grantSet map[string]map[Action]bool

// Policy 持有全部授权与组成员关系。零值不可用，须用 NewPolicy 构造。
type Policy struct {
	grants  map[Subject]grantSet
	members map[Subject]map[Subject]bool // group -> 直接成员
	parents map[Subject]map[Subject]bool // member -> 直接父组（反向索引）
}

// NewPolicy 创建空策略（默认拒绝：无任何授权）。
func NewPolicy() *Policy {
	return &Policy{
		grants:  make(map[Subject]grantSet),
		members: make(map[Subject]map[Subject]bool),
		parents: make(map[Subject]map[Subject]bool),
	}
}

// AddMember 把 member 加入 group。member 可以是用户或组（组可嵌套）。
func (p *Policy) AddMember(group, member Subject) {
	if p.members[group] == nil {
		p.members[group] = make(map[Subject]bool)
	}
	p.members[group][member] = true
	if p.parents[member] == nil {
		p.parents[member] = make(map[Subject]bool)
	}
	p.parents[member][group] = true
}

// RemoveMember 把 member 移出 group。
func (p *Policy) RemoveMember(group, member Subject) {
	delete(p.members[group], member)
	delete(p.parents[member], group)
}

// Grant 授予 subj 对 prop 的 a 权限。
func (p *Policy) Grant(subj Subject, prop string, a Action) {
	if p.grants[subj] == nil {
		p.grants[subj] = make(grantSet)
	}
	if p.grants[subj][prop] == nil {
		p.grants[subj][prop] = make(map[Action]bool)
	}
	p.grants[subj][prop][a] = true
}

// Revoke 撤销 subj 对 prop 的 a 权限；无授权时为空操作。
func (p *Policy) Revoke(subj Subject, prop string, a Action) {
	delete(p.grants[subj][prop], a)
}

// Direct 报告 subj 是否持有对 prop 的直接授权（不含组继承）。
func (p *Policy) Direct(subj Subject, prop string, a Action) bool {
	return p.grants[subj][prop][a]
}

// ParentsOf 返回 member 的直接父组。
func (p *Policy) ParentsOf(member Subject) []Subject {
	out := make([]Subject, 0, len(p.parents[member]))
	for g := range p.parents[member] {
		out = append(out, g)
	}
	return out
}
