package ontology

// WritePolicy 是类型统一声明的字段被拒写策略。同一类型内必须全局一致、不可混用。
type WritePolicy string

const (
	// PolicyRejectWhole 整条拒绝：任一被拒字段导致整次写入失败、不改任何字段。
	PolicyRejectWhole WritePolicy = "reject_whole"
	// PolicyIgnoreField 整体忽略：跳过被拒字段，其余字段照常写入。
	PolicyIgnoreField WritePolicy = "ignore_field"
)

// Op 是权限操作类型。
type Op string

const (
	OpRead  Op = "read"
	OpWrite Op = "write"
)

// AttrKind 是属性值类别（收紧类型约束时可进一步收窄，如 string -> enum）。
type AttrKind string

const (
	KindString AttrKind = "string"
	KindInt    AttrKind = "int"
	KindBool   AttrKind = "bool"
)

// Attribute 是某一版本快照中的属性描述。
// AttrID 为稳定标识符，弹性重命名只改 Name，标识符延续 => 权限随标识符继承。
type Attribute struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     AttrKind `json:"kind"`
	Required bool     `json:"required"`
}

// AttrChange 描述一次版本演进对单个属性的变更。
// Kind 取值：add / rename / tighten / deprecate。
type AttrChange struct {
	Kind    string   `json:"kind"`
	AttrID  string   `json:"attr_id"`
	NewName string   `json:"new_name,omitempty"`
	NewAttr AttrKind `json:"new_attr_kind,omitempty"`
}

const (
	ChangeAdd       = "add"
	ChangeRename    = "rename"
	ChangeTighten   = "tighten"
	ChangeDeprecate = "deprecate"
)

// TypeVersion 是对象类型的一个不可变版本。
type TypeVersion struct {
	TypeID      string       `json:"type_id"`
	Version     int          `json:"version"`
	Attrs       []Attribute  `json:"attrs"`
	Changes     []AttrChange `json:"changes"`
	WritePolicy WritePolicy  `json:"write_policy"`
}

// AttrSnapshot 记录标识符在某版本上的存活状态（供权限视图与审计使用）。
type AttrSnapshot struct {
	AttrID   string
	Name     string
	Alive    bool // false 表示该版本已废弃
	FirstVer int  // 标识符首次存在的版本
}

// PermissionEntry 是不可变的权限流水记录（授予或吊销），历史记录永不物理删除。
type PermissionEntry struct {
	Seq       int64  `json:"seq"`
	TypeID    string `json:"type_id"`
	Subject   string `json:"subject"`
	AttrID    string `json:"attr_id"` // "*" 表示类型级权限
	Op        Op     `json:"op"`
	Grant     bool   `json:"grant"` // true=授予 false=吊销
	FromVer   int    `json:"from_ver"`
	ToVer     int    `json:"to_ver"` // 闭区间；0 表示开放区间（随当前最新版本）
	OpenEnded bool   `json:"open_ended"`
}

// Covers 判断该条目版本闭区间（开放端按 atVersion 截断）是否覆盖版本 v。
func (e *PermissionEntry) Covers(v int) bool {
	if v < e.FromVer {
		return false
	}
	if !e.OpenEnded && v > e.ToVer {
		return false
	}
	return true
}

// Object 是对象实例；SchemaVer 记录其最后一次写入所基于的类型版本。
type Object struct {
	ID        string         `json:"id"`
	TypeID    string         `json:"type_id"`
	SchemaVer int            `json:"schema_ver"`
	Fields    map[string]any `json:"fields"`
}

// WriteRequest 是一次写请求。
type WriteRequest struct {
	ObjectID  string         `json:"object_id"`
	TypeID    string         `json:"type_id"`
	Subject   string         `json:"subject"`
	SchemaVer int            `json:"schema_ver"`
	Fields    map[string]any `json:"fields"` // key 可为属性标识符或当前名字
}

// FieldDeny 记录写请求中被拒绝的单个字段及其原因。
type FieldDeny struct {
	Ref  string    `json:"ref"`
	Op   Op        `json:"op"`
	Kind ErrorKind `json:"kind"`
	Msg  string    `json:"msg"`
}

// WriteResult 是写判定结果。
type WriteResult struct {
	Applied       bool        `json:"applied"`
	WrittenFields []string    `json:"written_fields"`
	IgnoredFields []string    `json:"ignored_fields"`
	Denies        []FieldDeny `json:"denies"`
	ObjectVersion int         `json:"object_version"`
	resolvedRefs  map[string]*Attribute
}

// ReadResult 是读判定结果。部分视图下被整体移除的属性不出现在对象中。
type ReadResult struct {
	Object       *Object  `json:"object"`
	PartialView  bool     `json:"partial_view"`
	RemovedAttrs []string `json:"removed_attrs"`
}

// DecisionLogEntry 是一次判定的完整审计日志：输入、输出与依据。
type DecisionLogEntry struct {
	Action string         `json:"action"`
	Input  map[string]any `json:"input"`
	Output map[string]any `json:"output"`
	Reason string         `json:"reason"`
}
