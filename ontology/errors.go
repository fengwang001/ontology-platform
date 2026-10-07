package ontology

import "errors"

// 四类对外暴露的判定结果。具体类型/属性不存在优先于其余三类。
var (
	// ErrNotFound 表示查询/操作的具体类型不存在，或该类型链条上不存在该属性。
	// 该判定优先于其余三类错误：其余三类都建立在类型与属性确实存在的前提上。
	ErrNotFound = errors.New("ontology: type or type-property combination not found")

	// ErrValueSetWidened 表示重新声明后的允许取值集合不是当前生效规则允许集合的子集。
	ErrValueSetWidened = errors.New("ontology: redeclared rule widens the allowed value set")

	// ErrSealedParent 表示试图以被标记为不允许派生（sealed）的类型为父类型创建子类型。
	ErrSealedParent = errors.New("ontology: cannot create subtype under a sealed type")

	// ErrTypeHasChildren 表示试图删除一个仍存在直接子类型依赖的中间（或非末端）类型。
	ErrTypeHasChildren = errors.New("ontology: cannot delete a type that still has subtypes")
)
