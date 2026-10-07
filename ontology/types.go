package ontology

// 本文件定义通用本体模型中的对象类型、链接类型与权限标识。
// 当前任务只需要类型层面的约束（方向、是否允许自环、是否允许同对象对间的多重
// 同类型链接），对象类型本身不携带属性模式。

// ObjectType 描述一类对象。
type ObjectType struct {
	ID string
}

// Direction 描述链接类型允许的遍历方向。
type Direction int

const (
	// Directed 表示链接只能从源对象向目标对象遍历。
	Directed Direction = iota
	// Bidirectional 表示链接允许沿两个方向遍历。
	Bidirectional
)

// LinkType 描述一类链接。
type LinkType struct {
	ID string
	// Direction 决定该类型链接允许的遍历方向。
	Direction Direction
	// AllowSelfLoop 为 false 时，源对象与目标对象相同的链接会被拒绝。
	AllowSelfLoop bool
	// AllowMultiple 为 false 时，同一对对象之间至多存在一条该类型的链接；
	// 为 true 时允许存在多重同类型链接（平行链接）。
	AllowMultiple bool
}

// Link 是两个对象之间的一条具体链接。
type Link struct {
	ID     string
	Type   *LinkType
	Source string
	Target string
}
