// Package ontology 实现基于角色与标签的访问控制模块。
//
// 标签不直接授予实例，而是挂在对象类型上，并沿链接类型声明的方向
// 在对象类型关系网络中传播；实例通过其所属对象类型继承标签。
// 角色对标签的允许/显式拒绝授权（含上级角色继承）共同决定主体
// 对实例的最终访问结论。
package ontology

// Direction 表示标签沿链接类型传播的方向。
type Direction int

const (
	// Downstream 沿链接边 from -> to 的方向传播。
	Downstream Direction = iota
	// Upstream 逆链接边方向（to -> from）传播。
	Upstream
)

func (d Direction) String() string {
	if d == Downstream {
		return "downstream"
	}
	return "upstream"
}

// Effect 表示角色对标签的授权效果。
type Effect int

const (
	// Allow 显式允许。
	Allow Effect = iota
	// Deny 显式拒绝。
	Deny
)

func (e Effect) String() string {
	if e == Deny {
		return "deny"
	}
	return "allow"
}

// ReasonCode 是判定结论的固定分类。错误类原因按如下唯一优先顺序汇报：
//
//	ReasonNotFound(1) > ReasonAllPathsBlocked(2) > ReasonDenyOverrides(3) > ReasonRoleCycle(4)
type ReasonCode string

const (
	// ReasonAllowed 全部相关标签均被允许。
	ReasonAllowed ReasonCode = "allowed"
	// ReasonTagNotPresent 被检查的标签未附着于该实例，因而不构成约束。
	ReasonTagNotPresent ReasonCode = "tag_not_present"
	// ReasonNoGrant 没有任何允许授权（默认拒绝）。
	ReasonNoGrant ReasonCode = "no_matching_allow"
	// ReasonExplicitDeny 存在显式拒绝且不存在允许。
	ReasonExplicitDeny ReasonCode = "explicit_deny"
	// ReasonNotFound 优先级 1：主体、标签或实例不存在。
	ReasonNotFound ReasonCode = "subject_or_tag_not_found"
	// ReasonAllPathsBlocked 优先级 2：标签的全部继承路径均被阻断。
	ReasonAllPathsBlocked ReasonCode = "all_paths_blocked"
	// ReasonDenyOverrides 优先级 3：显式拒绝优先于（隐式）允许。
	ReasonDenyOverrides ReasonCode = "deny_overrides_allow"
	// ReasonRoleCycle 优先级 4：角色层级存在循环继承，无法确定授权来源。
	ReasonRoleCycle ReasonCode = "role_hierarchy_cycle"
)

// reasonRank 给出错误原因的汇报优先级，数值越小优先级越高。
func reasonRank(c ReasonCode) int {
	switch c {
	case ReasonNotFound:
		return 1
	case ReasonAllPathsBlocked:
		return 2
	case ReasonDenyOverrides, ReasonExplicitDeny:
		return 3
	case ReasonRoleCycle:
		return 4
	default:
		return 100
	}
}

// LinkEdge 是对象类型之间的一条有向链接边。
type LinkEdge struct {
	LinkType string
	From     string // 对象类型 ID
	To       string // 对象类型 ID
}

// Propagation 声明某标签可沿某链接类型向指定方向传播，深度不设上限。
type Propagation struct {
	Tag       string
	LinkType  string
	Direction Direction
}

// Attachment 是标签在对象类型上的直接挂载，也是传播的源头。
type Attachment struct {
	ObjectType string
	Tag        string
}

// Block 是显式传播阻断点：标签到达该对象类型后不再继续向外传播，
// 但该对象类型自身仍携带该标签。
type Block struct {
	ObjectType string
	Tag        string
}

// Grant 是角色对标签的授权声明。
type Grant struct {
	Role   string
	Tag    string
	Effect Effect
}

// TagSource 记录某对象类型携带某标签的来源证据（用于日志与审计）。
type TagSource struct {
	Attachment Attachment    // 源头直接挂载
	Via        []Propagation // 传播途中使用到的传播声明
}

// GrantEvidence 记录一次授权裁决所依据的角色声明。
type GrantEvidence struct {
	Role      string // 声明该授权的角色
	Tag       string
	Effect    Effect
	Inherited bool // true 表示来自上级角色继承
}

// TagVerdict 是主体对实例上单个标签的裁决结果。
type TagVerdict struct {
	Tag     string
	Allowed bool
	Reason  ReasonCode
	Grants  []GrantEvidence
	Sources []TagSource // 该标签在该对象类型上的继承来源
}

// DecisionStats 是判定开销的可观测证明：一次判定遍历的标签来源数
// 与角色层级节点数。该指标不随关系网络总规模增长。
type DecisionStats struct {
	TagSourcesVisited int
	RoleNodesVisited  int
}

// Decision 是一次访问判定的最终输出。
type Decision struct {
	Subject  string
	Instance string
	Tag      string // 仅 DecideTag 非空
	Allowed  bool
	Reason   ReasonCode
	Detail   string
	Verdicts []TagVerdict
	Stats    DecisionStats
}
