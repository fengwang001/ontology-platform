// Package delegation implements the role delegation chain module of the
// ontology platform.
//
// 角色持有者可以将其在某些对象类型上的属性级与行级权限的子集，
// 在有限有效期内委托给其他主体，并标记是否允许再委托。
// 委托链上任一环的原始权限收缩会级联使下游委托失效；
// 历史访问判定结果不可被后续变更追溯改变。
package delegation

import "time"

// ObjectPerm 表示某个对象类型上的一份权限：
// 属性级权限（可访问的属性集合）与行级权限（可访问的行集合）。
type ObjectPerm struct {
	Attrs map[string]bool `json:"attrs,omitempty"`
	Rows  map[string]bool `json:"rows,omitempty"`
}

// PermissionSet 表示一组对象类型上的权限，key 为对象类型标识。
type PermissionSet map[string]ObjectPerm

// DelegationID 是委托记录的唯一标识。
type DelegationID uint64

// Delegation 是一条委托记录。
type Delegation struct {
	ID              DelegationID  `json:"id"`
	Delegator       string        `json:"delegator"`
	Delegatee       string        `json:"delegatee"`
	Subset          PermissionSet `json:"subset"`
	Start           time.Time     `json:"start"`
	End             time.Time     `json:"end"`
	AllowRedelegate bool          `json:"allow_redelegate"`
	CreatedAt       time.Time     `json:"created_at"`
	// RevokedAt 非 nil 表示该委托已在该时刻被撤销。
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// DelegationRequest 是声明一条新委托的入参。
type DelegationRequest struct {
	Delegator       string        `json:"delegator"`
	Delegatee       string        `json:"delegatee"`
	Subset          PermissionSet `json:"subset"`
	Start           time.Time     `json:"start"`
	End             time.Time     `json:"end"`
	AllowRedelegate bool          `json:"allow_redelegate"`
}

// Decision 是一次访问判定的结果。
type Decision struct {
	Allowed bool `json:"allowed"`
	// Witness 是支持本次放行的委托链依据（委托记录 ID 路径，
	// 从被判定主体一路回溯到原始权限持有者）；若仅凭原始权限
	// 放行则为空切片。拒绝时为空。
	Witness []DelegationID `json:"witness,omitempty"`
	// NodesVisited 是本次判定实际检查过的委托记录条数，
	// 用于可观测地证明判定开销与系统中委托记录总数无关。
	NodesVisited int `json:"nodes_visited"`
}

// AccessRequest 是一次访问判定查询的入参。
type AccessRequest struct {
	Subject    string     `json:"subject"`
	ObjectType string     `json:"object_type"`
	Require    ObjectPerm `json:"require"`
	At         time.Time  `json:"at"`
}
