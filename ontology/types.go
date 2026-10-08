package ontology

// ObjectID 唯一标识一个对象实例。
type ObjectID string

// ObjectTypeID 标识对象类型。
type ObjectTypeID string

// LinkTypeID 标识链接类型。
type LinkTypeID string

// UserID 标识调用者。
type UserID string

// AdminUser 是内置超级用户，隐式拥有全部权限，用于引导授权。
const AdminUser UserID = "admin"

// Action 表示权限动作。
type Action string

const (
	ActionRead  Action = "read"
	ActionWrite Action = "write"
)

// ResourceKind 权限资源的类别。
type ResourceKind string

const (
	ResourceObjectType ResourceKind = "object_type"
	ResourceLinkType   ResourceKind = "link_type"
)

// Resource 指向一个可被授权的资源。
type Resource struct {
	Kind ResourceKind `json:"kind"`
	ID   string       `json:"id"`
}

// Permission 表示一次授权记录。
type Permission struct {
	User     UserID   `json:"user"`
	Action   Action   `json:"action"`
	Resource Resource `json:"resource"`
}

// LinkType 声明一类链接的方向、代价与基数上限。
type LinkType struct {
	ID       LinkTypeID   `json:"id"`
	FromType ObjectTypeID `json:"from_type"`
	ToType   ObjectTypeID `json:"to_type"`
	Cost     int64        `json:"cost"` // 必须 > 0
	Directed bool         `json:"directed"`
	// MaxOutgoing/MaxIncoming 为每个源/目标对象允许的最大链接数，0 表示不限。
	MaxOutgoing int `json:"max_outgoing"`
	MaxIncoming int `json:"max_incoming"`
}

// ObjectType 声明一类对象。
type ObjectType struct {
	ID ObjectTypeID `json:"id"`
}

// OpKind 变更操作类别。
type OpKind string

const (
	OpDeclareObjectType OpKind = "declare_object_type"
	OpDeclareLinkType   OpKind = "declare_link_type"
	OpCreateObject      OpKind = "create_object"
	OpInvalidateObject  OpKind = "invalidate_object"
	OpCreateLink        OpKind = "create_link"
	OpRemoveLink        OpKind = "remove_link"
	OpGrantPermission   OpKind = "grant_permission"
	OpRevokePermission  OpKind = "revoke_permission"
)

// Operation 是一次改变图状态的请求。只有被接受的操作才会进入日志。
type Operation struct {
	Kind       OpKind       `json:"kind"`
	ObjectType ObjectTypeID `json:"object_type,omitempty"`
	Object     ObjectID     `json:"object,omitempty"`
	LinkType   LinkTypeID   `json:"link_type,omitempty"`
	From       ObjectID     `json:"from,omitempty"`
	To         ObjectID     `json:"to,omitempty"`
	LinkSpec   *LinkType    `json:"link_spec,omitempty"`
	Permission *Permission  `json:"permission,omitempty"`
}

// LogEntry 是持久化日志项，Seq 严格单调递增且无空洞。
type LogEntry struct {
	Seq uint64    `json:"seq"`
	Op  Operation `json:"op"`
}

// RejectKind 四级拒绝分类；ErrNotReady 不属于该次序。
type RejectKind string

const (
	RejectInvalidParams RejectKind = "invalid_params"
	RejectPermission    RejectKind = "permission_denied"
	RejectCardinality   RejectKind = "cardinality_exceeded"
)

// RejectError 是被拒绝的变更操作错误，携带四级分类之一。
type RejectError struct {
	Kind   RejectKind
	Detail string
}

func (e *RejectError) Error() string { return string(e.Kind) + ": " + e.Detail }

// ErrNotReady 在重放未完成时返回，独立于四级拒绝次序，不进入日志。
var ErrNotReady = &notReadyError{}

type notReadyError struct{}

func (e *notReadyError) Error() string { return "service_not_ready: replay in progress" }

// IsNotReady 判断错误是否为「服务未就绪」。
func IsNotReady(err error) bool {
	_, ok := err.(*notReadyError)
	return ok
}

// Path 是一次最短路径查询的确定结果。
type Path struct {
	Objects []ObjectID `json:"objects"`
	Cost    int64      `json:"cost"`
	Found   bool       `json:"found"`
}

// Metrics 是内部可验证的查询度量，只统计本次查询实际访问的对象与链接数，
// 不对调用者暴露（HTTP 层不返回）。
type Metrics struct {
	VisitedObjects int
	VisitedLinks   int
}
