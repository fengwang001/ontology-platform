package ontology

// Rep 描述字段的重复性。
type Rep int

const (
	Required Rep = iota
	Optional
	Repeated
)

func (r Rep) String() string {
	switch r {
	case Required:
		return "REQUIRED"
	case Optional:
		return "OPTIONAL"
	case Repeated:
		return "REPEATED"
	default:
		return "UNKNOWN"
	}
}

// Field 是模式节点。Children 为空表示叶子（int64 值），否则为分组。
type Field struct {
	Name     string
	Rep      Rep
	Children []Field
}

// Entry 是叶子列上的一个条目：重复级 rep、定义级 def，叶子值仅在 def==maxDef 时有效。
type Entry struct {
	Rep   int
	Def   int
	Value int64
}

// PageInfo 描述一个列页：起始记录序号、记录数、条目数。
type PageInfo struct {
	StartRecord int
	RecordCount int
	EntryCount  int
}

// Stats 是列统计：空条目数、非空条目数、最小值、最大值。
type Stats struct {
	NullCount    int
	PresentCount int
	Min          int64
	Max          int64
}
