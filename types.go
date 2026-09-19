package ontology

// ObjectKey 唯一标识一个对象实例（ObjectType + 主键）。
type ObjectKey struct {
	Type string
	ID   string
}

// Cardinality 描述 LinkType 的基数约束。
type Cardinality int

const (
	// OneToOne 每个源至多一条出链，每个目标至多一条入链。
	OneToOne Cardinality = iota
	// OneToMany 每个源可有多条出链，每个目标至多一条入链。
	OneToMany
	// ManyToMany 两侧均不受限。
	ManyToMany
)

func (c Cardinality) String() string {
	switch c {
	case OneToOne:
		return "ONE_TO_ONE"
	case OneToMany:
		return "ONE_TO_MANY"
	case ManyToMany:
		return "MANY_TO_MANY"
	default:
		return "UNKNOWN"
	}
}

// CascadeMode 描述删除对象时沿链的级联语义。
type CascadeMode int

const (
	// CascadeDelete 连带删除对端对象，并继续触发对端自身的级联。
	CascadeDelete CascadeMode = iota
	// CascadeSetNull 断开链但保留对端对象。
	CascadeSetNull
	// CascadeRestrict 存在链时拒绝删除，整次级联回滚。
	CascadeRestrict
)

func (m CascadeMode) String() string {
	switch m {
	case CascadeDelete:
		return "CASCADE"
	case CascadeSetNull:
		return "SET_NULL"
	case CascadeRestrict:
		return "RESTRICT"
	default:
		return "UNKNOWN"
	}
}

// LinkType 声明一类关系：名字、源/目标 ObjectType、基数、
// 两侧必选性与删除时的级联语义。源与目标可以是同一个 ObjectType（自引用）。
type LinkType struct {
	Name string

	SourceType string
	TargetType string

	Cardinality Cardinality

	// SourceRequired 为真时，每个 SourceType 对象必须至少有一条出链（提交时校验）。
	SourceRequired bool
	// TargetRequired 为真时，每个 TargetType 对象必须至少有一条入链（提交时校验）。
	TargetRequired bool

	// Cascade 决定删除任一侧端点对象时的级联行为。
	Cascade CascadeMode
}

// Link 表示一条具体的关系实例。
type Link struct {
	LinkType string
	Source   ObjectKey
	Target   ObjectKey
}

// PathStep 描述级联路径或环上的一步。
type PathStep struct {
	LinkType string
	Source   ObjectKey
	Target   ObjectKey
}
