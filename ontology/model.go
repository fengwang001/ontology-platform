package ontology

// ObjectID 是对象的全局唯一标识。
type ObjectID string

// LinkTypeID 标识链接类型。
type LinkTypeID string

// LinkDirection 描述链接类型允许的遍历方向。
type LinkDirection int

const (
	// DirectionOut 仅允许沿 from -> to 方向遍历。
	DirectionOut LinkDirection = iota
	// DirectionIn 仅允许沿 to -> from 方向遍历。
	DirectionIn
	// DirectionBoth 双向均可遍历（无向语义）。
	DirectionBoth
)

// ObjectType 定义对象类型。当前遍历只依赖对象标识，类型信息用于通用本体模型完整性。
type ObjectType struct {
	ID   string
	Name string
}

// LinkType 定义链接类型，含方向与代价。
type LinkType struct {
	ID        LinkTypeID
	Direction LinkDirection
	// Cost 是链接类型的遍历代价（元数据，当前上限按跳数而非代价计）。
	Cost int
}

// Object 是本体中的一个对象实例。
type Object struct {
	ID   ObjectID
	Type string
}

// Link 是两个对象之间的一条具类型链接。
type Link struct {
	Type LinkTypeID
	From ObjectID
	To   ObjectID
}

// Principal 是调用者身份。
type Principal struct {
	Name string
}
