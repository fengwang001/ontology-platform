package ontology

// Visibility 表示某个操作者对某实例上某个属性的最终可见性。
type Visibility int

const (
	// Invisible 表示不可见；Visible 表示可见。
	Invisible Visibility = iota
	Visible
)

func (v Visibility) String() string {
	switch v {
	case Visible:
		return "VISIBLE"
	default:
		return "INVISIBLE"
	}
}

// SubjectKind 标识一条实例层覆盖声明挂在操作者身上还是角色身上。
type SubjectKind int

const (
	SubjectOperator SubjectKind = iota
	SubjectRole
)

func (k SubjectKind) String() string {
	switch k {
	case SubjectRole:
		return "ROLE"
	default:
		return "OPERATOR"
	}
}

// ErrorCode 是归一化后的拒绝原因类别。
type ErrorCode string

const (
	// ErrInvalidParam 参数非法，创建与删除的第一优先级。
	ErrInvalidParam ErrorCode = "ERR_INVALID_PARAM"
	// ErrInvisible 操作者对至少一端来源属性不可见（仅创建路径报告）。
	ErrInvisible ErrorCode = "ERR_INVISIBLE"
	// ErrSrcCardinality 起点一侧基数超限。
	ErrSrcCardinality ErrorCode = "ERR_SOURCE_CARDINALITY_EXCEEDED"
	ErrTgtCardinality ErrorCode = "ERR_TARGET_CARDINALITY_EXCEEDED"
	// ErrNotFound 链接不存在；删除路径下“因不可见而视同不存在”也归这一类。
	ErrNotFound ErrorCode = "ERR_NOT_FOUND"
)

// ArbError 携带可区分的错误类别与人类可读说明。
type ArbError struct {
	Code ErrorCode
	Msg  string
}

func (e *ArbError) Error() string { return string(e.Code) + ": " + e.Msg }

// AttrSpec 在注册对象类型时声明属性及其类型层默认可见性。
type AttrSpec struct {
	Name    string
	Default Visibility
}

// LinkTypeSpec 声明链接类型：两端对象类型、参与链接的来源属性及各自基数上限。
// 基数上限 <= 0 表示不限制。
type LinkTypeSpec struct {
	Name             string
	SrcType, SrcAttr string
	TgtType, TgtAttr string
	SrcMax, TgtMax   int
}

// Link 是一条已经物理存在的链接。
type Link struct {
	ID         string
	TypeName   string
	SrcID      string
	TgtID      string
	CreatedSeq int
}

// Command 是可被仲裁器串行应用的全部命令的密封接口。
type Command interface {
	commandMarker()
}

type RegisterObjectType struct {
	Name  string
	Attrs []AttrSpec
}

type SetTypeDefault struct {
	TypeName string
	Attr     string
	Vis      Visibility
}

type RegisterLinkType struct {
	Spec LinkTypeSpec
}

type CreateInstance struct {
	ID       string
	TypeName string
}

type AddRole struct {
	Role string
}

// IncludeRole 声明 Child 角色包含 Parent 角色（Child 的成员沿层级可到达 Parent）。
type IncludeRole struct {
	Child  string
	Parent string
}

type AssignRole struct {
	Operator string
	Role     string
}

// DeclareOverride 在实例层为某属性声明覆盖；主体可以是操作者本人（距离 0）或角色。
type DeclareOverride struct {
	InstanceID string
	Attr       string
	Kind       SubjectKind
	Subject    string
	Vis        Visibility
}

type CreateLink struct {
	ID       string
	TypeName string
	SrcID    string
	TgtID    string
	Operator string
}

type DeleteLink struct {
	ID       string
	Operator string
}

func (RegisterObjectType) commandMarker() {}
func (SetTypeDefault) commandMarker()     {}
func (RegisterLinkType) commandMarker()   {}
func (CreateInstance) commandMarker()     {}
func (AddRole) commandMarker()            {}
func (IncludeRole) commandMarker()        {}
func (AssignRole) commandMarker()         {}
func (DeclareOverride) commandMarker()    {}
func (CreateLink) commandMarker()         {}
func (DeleteLink) commandMarker()         {}
