// Package acl 定义主体模型与授权存储。
//
// 主体是用户或组；组有成员（用户或子组，可嵌套）。授权是三元组
// (主体, 属性, 读|写)，可授予（Grant）、显式拒绝（Deny）或撤销（Revoke）。
// 默认策略为默认拒绝（见 DESIGN.md 推导点 1）：无显式授权即不允许。
package acl

// Kind 区分主体类型。
type Kind int

const (
	KindUser Kind = iota
	KindGroup
)

// Subject 是一个用户或组主体。
type Subject struct {
	Kind Kind
	ID   string
}

// User 构造用户主体。
func User(id string) Subject { return Subject{Kind: KindUser, ID: id} }

// Group 构造组主体。
func Group(id string) Subject { return Subject{Kind: KindGroup, ID: id} }

// Action 是读或写动作。
type Action int

const (
	Read Action = iota
	Write
)

// grantKey 定位一条授权记录。
type grantKey struct {
	subject  Subject
	property string
	action   Action
}

// Store 保存组成员关系与授权记录。零值不可用，须用 NewStore。
type Store struct {
	members map[Subject][]Subject // 组 -> 直接成员
	parents map[Subject][]Subject // 成员 -> 直接所在的组（反向索引）
	grants  map[grantKey]bool     // 显式授权记录：true=允许，false=显式拒绝
}

// NewStore 创建空的授权存储。
func NewStore() *Store {
	return &Store{
		members: make(map[Subject][]Subject),
		parents: make(map[Subject][]Subject),
		grants:  make(map[grantKey]bool),
	}
}

// AddMember 把 member 加入 group。member 可以是用户或子组（组可嵌套）。
func (s *Store) AddMember(group, member Subject) {
	for _, m := range s.members[group] {
		if m == member {
			return
		}
	}
	s.members[group] = append(s.members[group], member)
	s.parents[member] = append(s.parents[member], group)
}

// Members 返回组的直接成员。
func (s *Store) Members(group Subject) []Subject {
	return append([]Subject(nil), s.members[group]...)
}

// Parents 返回主体直接所在的组。
func (s *Store) Parents(member Subject) []Subject {
	return append([]Subject(nil), s.parents[member]...)
}

// Grant 授予 subject 对 property 的 action 权限。
func (s *Store) Grant(subject Subject, property string, action Action) {
	s.grants[grantKey{subject, property, action}] = true
}

// Deny 显式拒绝 subject 对 property 的 action 权限。
// 直接拒绝优先于任何组继承的允许（直接授权覆盖组授权）。
func (s *Store) Deny(subject Subject, property string, action Action) {
	s.grants[grantKey{subject, property, action}] = false
}

// Revoke 撤销 subject 对 property 的 action 的显式记录（允许或拒绝），
// 使该三元组回到「无显式授权」状态，即回到默认拒绝/组继承判定。
func (s *Store) Revoke(subject Subject, property string, action Action) {
	delete(s.grants, grantKey{subject, property, action})
}

// direct 查询主体自身的显式授权记录；第二个返回值表示是否存在记录。
func (s *Store) direct(subject Subject, property string, action Action) (bool, bool) {
	allowed, ok := s.grants[grantKey{subject, property, action}]
	return allowed, ok
}

// DirectAllowed 暴露 direct 给同模块的 access 包之外的调用者做判定：
// 返回主体自身的显式授权结果，以及该记录是否存在。
func (s *Store) DirectAllowed(subject Subject, property string, action Action) (allowed, exists bool) {
	return s.direct(subject, property, action)
}
