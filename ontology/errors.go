package ontology

import "errors"

var (
	// ErrNotFound 表示具体类型或属性组合不存在。
	// 在各类操作中，该判定优先于其余校验给出。
	ErrNotFound = errors.New("ontology: type or property not found")

	// ErrRuleWidening 表示属性重新声明的取值集合相对生效规则扩大，被拒绝。
	ErrRuleWidening = errors.New("ontology: rule redeclaration widens allowed value set")

	// ErrTypeSealed 表示试图在不允许派生的类型上创建子类型，被拒绝。
	ErrTypeSealed = errors.New("ontology: parent type is sealed and forbids subtyping")

	// ErrTypeHasChildren 表示试图删除仍存在子类型依赖的中间类型，被拒绝。
	ErrTypeHasChildren = errors.New("ontology: type has subtypes and cannot be deleted")

	// ErrValueNotAllowed 表示写入值不在当前生效规则允许的取值集合内。
	ErrValueNotAllowed = errors.New("ontology: value not allowed by effective rule")
)
