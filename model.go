package ontology

// Visibility 表示某个操作者对某个实例上某个链接来源属性的最终可见性。
type Visibility int8

const (
	// Visible 可见：允许看到链接存在并参与创建/删除仲裁。
	Visible Visibility = iota
	// Invisible 不可见：链接存在性对该操作者整体隐藏。
	Invisible
)

func (v Visibility) String() string {
	switch v {
	case Visible:
		return "visible"
	case Invisible:
		return "invisible"
	default:
		return "unknown"
	}
}

// PropertySpec 声明对象类型上的一个属性。LinkSource 为 true 时该属性
// 可以作为链接类型的端点来源属性；DefaultVis 是类型层默认可见性。
type PropertySpec struct {
	Name       string
	LinkSource bool
	DefaultVis Visibility
}

// EndpointSpec 声明链接类型的一个端点：对象类型上的某个链接来源属性。
type EndpointSpec struct {
	ObjectType string
	Property   string
}

// LinkTypeSpec 声明一个链接类型及其两端基数上限（0 表示不限）。
type LinkTypeSpec struct {
	Name      string
	Source    EndpointSpec
	Target    EndpointSpec
	SourceMax int
	TargetMax int
}

// Instance 是对象类型的一个具体实例。
type Instance struct {
	ID   string
	Type string
}

// Link 是一条已经创建的具体链接。
type Link struct {
	LinkType       string
	SourceInstance string
	TargetInstance string
}

// Key 返回链接在全局链接集合中的唯一键。
func (l Link) Key() string {
	return l.LinkType + "\x00" + l.SourceInstance + "\x00" + l.TargetInstance
}

func (l Link) String() string {
	return l.LinkType + "(" + l.SourceInstance + "->" + l.TargetInstance + ")"
}

// ErrorClass 是四类（删除场景三类）可相互区分的归一化错误类别。
type ErrorClass int

const (
	// ErrOK 仲裁通过。
	ErrOK ErrorClass = iota
	// ErrInvalidArg 参数非法（类型/属性/实例未声明、端点不匹配等），优先级最高。
	ErrInvalidArg
	// ErrPermissionDenied 操作者对任一端点来源属性不可见。
	ErrPermissionDenied
	// ErrSourceCardExceeded 起点一侧基数超限。
	ErrSourceCardExceeded
	// ErrTargetCardExceeded 终点一侧基数超限。
	ErrTargetCardExceeded
	// ErrNotFound 删除场景：链接不存在与「对操作者不可见」合并为同一类别。
	ErrNotFound
)

func (c ErrorClass) String() string {
	switch c {
	case ErrOK:
		return "ok"
	case ErrInvalidArg:
		return "invalid_argument"
	case ErrPermissionDenied:
		return "permission_denied"
	case ErrSourceCardExceeded:
		return "source_cardinality_exceeded"
	case ErrTargetCardExceeded:
		return "target_cardinality_exceeded"
	case ErrNotFound:
		return "not_found"
	default:
		return "unknown"
	}
}

// DecisionError 是联合仲裁器归一化后的拒绝结论。
type DecisionError struct {
	Class   ErrorClass
	Message string
}

func (e *DecisionError) Error() string {
	if e == nil {
		return "ok"
	}
	return e.Class.String() + ": " + e.Message
}

// resolveResult 是权限继承/覆盖解析的内部结果。
type resolveResult struct {
	vis         Visibility
	distance    int    // 操作者到提供覆盖的主体的层级距离；使用默认时为 -1
	seq         int64  // 同距离时取更晚声明（更大 seq）
	source      string // 提供该覆盖的操作者或角色；使用默认时为空
	usedDefault bool
}
