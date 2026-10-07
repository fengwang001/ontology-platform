package ontology

import "errors"

// 拒绝判定次序（由高到低）：
//  1. 参数非法（ErrInvalidArgument）
//  2. 起点对象无存在性权限（ErrForbidden）
//  3. 续读标记锚定快照中的起点已不可追溯（ErrTokenObsolete）
//  4. 正常分页
var (
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	ErrForbidden       = errors.New("ontology: start object is not traversable for caller")
	ErrTokenObsolete   = errors.New("ontology: continuation token points to a start object that no longer exists in its anchored snapshot")
	ErrTokenInvalid    = errors.New("ontology: malformed continuation token")
)
