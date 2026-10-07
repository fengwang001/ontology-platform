package traversal

import "errors"

// 输入校验错误按固定次序判定，一次只返回第一类错误：
// 1. 起始对象不存在；
// 2. 方向集合为空、方向值非法、或包含未在图上登记的链接类型；
// 3. 深度上限非正整数。
var (
	ErrStartObjectNotFound = errors.New("traversal: start object does not exist")
	ErrInvalidDirections   = errors.New("traversal: directions are empty or reference an undefined link type")
	ErrInvalidDepth        = errors.New("traversal: max depth must be a positive integer")
)

// 图结构修改错误。
var (
	ErrDuplicateObject   = errors.New("traversal: object with the same id already exists")
	ErrDuplicateLink     = errors.New("traversal: link with the same id already exists")
	ErrDuplicateLinkType = errors.New("traversal: link type with the same id already exists")
	ErrObjectMissing     = errors.New("traversal: link endpoint object does not exist")
	ErrLinkTypeMissing   = errors.New("traversal: link type is not defined")
)
