package ontology

// Policy 定义权限模型：
//   - 存在性权限：调用者是否“看得见”某对象；
//   - 遍历权限：调用者是否可以沿某条链接扩展。
//
// 判定基于快照发生，保证续读标记锚定的旧快照下权限视图也被冻结。
type Policy interface {
	CanSeeObject(snap *Snapshot, caller *Principal, id ObjectID) bool
	CanTraverseLink(snap *Snapshot, caller *Principal, link Link) bool
}

// AllowAllPolicy 允许一切（存在性与遍历均放行），主要用于测试与默认场景。
type AllowAllPolicy struct{}

func (AllowAllPolicy) CanSeeObject(*Snapshot, *Principal, ObjectID) bool { return true }

func (AllowAllPolicy) CanTraverseLink(*Snapshot, *Principal, Link) bool { return true }

// ACLPolicy 是一个显式名单权限模型：
//   - hidden 中的对象对指定调用者不存在（无存在性权限）；
//   - blocked 中的链接（按 from|type|to 编码）对指定调用者不可遍历。
//
// 名单在创建时确定且不可变，从而在任何快照上给出一致判定。
type ACLPolicy struct {
	hiddenObjects map[principalKey]map[ObjectID]bool
	blockedLinks  map[principalKey]map[linkKey]bool
}

type principalKey string
type linkKey string

func makeLinkKey(l Link) linkKey {
	return linkKey(string(l.From) + "|" + string(l.Type) + "|" + string(l.To))
}

// NewACLPolicy 基于隐藏对象与封禁链接名单创建权限策略。
func NewACLPolicy(
	hidden map[*Principal][]ObjectID,
	blocked map[*Principal][]Link,
) *ACLPolicy {
	p := &ACLPolicy{
		hiddenObjects: map[principalKey]map[ObjectID]bool{},
		blockedLinks:  map[principalKey]map[linkKey]bool{},
	}
	for caller, ids := range hidden {
		k := principalKey(caller.Name)
		set := p.hiddenObjects[k]
		if set == nil {
			set = map[ObjectID]bool{}
			p.hiddenObjects[k] = set
		}
		for _, id := range ids {
			set[id] = true
		}
	}
	for caller, links := range blocked {
		k := principalKey(caller.Name)
		set := p.blockedLinks[k]
		if set == nil {
			set = map[linkKey]bool{}
			p.blockedLinks[k] = set
		}
		for _, l := range links {
			set[makeLinkKey(l)] = true
		}
	}
	return p
}

func (p *ACLPolicy) key(caller *Principal) principalKey {
	if caller == nil {
		return ""
	}
	return principalKey(caller.Name)
}

// CanSeeObject 判定对象存在性权限。不存在于快照中的对象也视为不可见。
func (p *ACLPolicy) CanSeeObject(snap *Snapshot, caller *Principal, id ObjectID) bool {
	if _, ok := snap.objects[id]; !ok {
		return false
	}
	return !p.hiddenObjects[p.key(caller)][id]
}

// CanTraverseLink 判定链接遍历权限。
func (p *ACLPolicy) CanTraverseLink(snap *Snapshot, caller *Principal, link Link) bool {
	return !p.blockedLinks[p.key(caller)][makeLinkKey(link)]
}
