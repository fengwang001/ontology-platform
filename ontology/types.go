// Package ontology 提供本体平台的双时态链接历史一致性审计子系统。
//
// 子系统围绕链接类型（LinkType）维护双时态（有效时间 + 记录时间）的
// 链接创建/撤销事实轨迹，支持在任意历史记录时刻回放当时实际存在的链接
// 集合，并依据该记录时刻生效的基数约束版本进行纯只读的历史审计。
package ontology

// ObjectTypeID 对象类型标识。
type ObjectTypeID string

// ObjectID 对象实例标识。
type ObjectID string

// LinkTypeID 链接类型标识。
type LinkTypeID string

// openEnd 表示有效时间区间的开放端点（尚未撤销）。
const openEnd = int64(^uint64(0) >> 1)

// Cardinality 单侧基数约束：该侧每个对象允许拥有的链接数量范围。
// Max 取 -1 表示无上限；Min 仅对至少拥有一条链接的对象生效（见设计文档）。
type Cardinality struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// unconstrained 表示不施加任何基数限制。
var unconstrained = Cardinality{Min: 0, Max: -1}

// violated 判断给定度数是否违反该基数约束。
// degree 为 0 的对象不参与 Min 检查（Min 约束只针对已参与链接的对象）。
func (c Cardinality) violated(degree int) bool {
	if degree == 0 {
		return false
	}
	if degree < c.Min {
		return true
	}
	return c.Max >= 0 && degree > c.Max
}

// ConstraintVersion 基数约束的一个版本，自 EffectiveFrom（记录时刻）起生效。
type ConstraintVersion struct {
	Version       int         `json:"version"`
	EffectiveFrom int64       `json:"effectiveFrom"`
	Left          Cardinality `json:"left"`
	Right         Cardinality `json:"right"`
}

// LinkTypeDef 链接类型定义：两端对象类型与是否对称。
type LinkTypeDef struct {
	ID        LinkTypeID   `json:"id"`
	LeftType  ObjectTypeID `json:"leftType"`
	RightType ObjectTypeID `json:"rightType"`
	Symmetric bool         `json:"symmetric"`
}
