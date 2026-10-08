// Package ontology 实现本体链接图上的跨类型最短路径查询。
//
// 对象类型集合固定，每个对象实例属于恰好一个对象类型；链接类型定义两端
// 允许的对象类型、方向、所属类别与非负整数步进代价。查询在类别约束序列、
// 权限可见性与逻辑隔离的共同约束下，寻找总代价最小的简单路径。
package ontology

import "errors"

// ObjectID 是对象实例标识，按字典序参与平局消解。
type ObjectID string

// ObjectTypeID 是对象类型标识。
type ObjectTypeID string

// LinkTypeID 是链接类型标识。
type LinkTypeID string

// LinkID 是链接实例标识。
type LinkID string

// Category 是链接类别标识，其全序由 NewGraph 时给定的类别序列决定。
type Category string

// Principal 是权限主体标识。
type Principal string

var (
	// ErrInvalidParams 表示查询参数非法（起点/终点不存在、约束序列为空、
	// 重复标记出现在中间位置、约束引用了未知类别等）。
	ErrInvalidParams = errors.New("ontology: invalid query parameters")
	// ErrForbiddenObjectType 表示起点或终点所在对象类型被禁止参与路径查询。
	ErrForbiddenObjectType = errors.New("ontology: object type forbidden in path queries")
)

// MaxLinkCost 是链接类型步进代价的上限。
const MaxLinkCost int64 = 1_000_000

// ObjectTypeSpec 描述一个对象类型。对象类型集合在 NewGraph 时一次性给定。
type ObjectTypeSpec struct {
	ID ObjectTypeID
	// ForbiddenInPathQuery 为 true 时，该类型的对象不得参与路径查询：
	// 作为起点或终点会导致查询被拒绝，作为中间节点会被搜索跳过。
	ForbiddenInPathQuery bool
}

// LinkTypeSpec 描述一个链接类型。
type LinkTypeSpec struct {
	ID LinkTypeID
	// From/To 是两端允许的对象类型。单向链接要求 From 端对象类型为 From、
	// To 端为 To；双向链接允许两种端点类型按任意一端出现。
	From ObjectTypeID
	To   ObjectTypeID
	// Bidirectional 为 true 时链接可沿两个方向遍历，否则只能 From -> To。
	Bidirectional bool
	// Category 是该类型所有链接实例的类别。
	Category Category
	// Cost 是每条该类型链接实例的步进代价，取值 [0, MaxLinkCost]。
	Cost int64
	// RestrictedTo 非空时，仅列出的权限主体可见该类型的链接；
	// 为空表示所有查询者可见。
	RestrictedTo []Principal
}

// PatternElem 是类别约束序列中的一个位置。
type PatternElem struct {
	// Any 为 true 表示该位置可匹配任意类别，否则必须匹配 Category。
	Any bool
	// Category 在 Any 为 false 时指定必须匹配的类别。
	Category Category
	// Star 为 true 表示该位置的元素可重复零次或多次。
	// 仅允许出现在序列两端（第一个或最后一个位置）。
	Star bool
}

// PathQuery 是一次最短路径查询的输入。
type PathQuery struct {
	Start     ObjectID
	End       ObjectID
	Pattern   []PatternElem
	Principal Principal
}

// PathResult 是一次最短路径查询的结果。
type PathResult struct {
	// Found 为 false 表示不可达（合法结果，不是错误）。
	Found bool
	// Equivalent 为 true 表示存在多条（代价、类别序列、对象序列）完全相同
	// 的最优路径（仅链接实例不同）；此时返回链接标识序列最小的代表，
	// 不会随机选择。
	Equivalent bool
	// Cost 是最优路径的总代价。
	Cost int64
	// Objects 是路径经过的对象标识序列（含起点与终点）。
	Objects []ObjectID
	// Links 是路径经过的链接实例标识序列。
	Links []LinkID
	// Categories 是路径的链接类别序列。
	Categories []Category
}
