package infer

import "errors"

// 会话操作可能返回的可区分拒绝原因。除 ErrInvalidArgument 外，
// 每个原因都对应规格中一种独立的拒绝情形；调用方用 errors.Is 判定。
var (
	// ErrInvalidArgument 表示参数非法：未登记的构造子名字、元数不符、
	// 未知变量编号、空名字，或构造参数本身越界。
	ErrInvalidArgument = errors.New("infer: invalid argument")
	// ErrLevelZero 表示 Leave 时当前层级已为 0。
	ErrLevelZero = errors.New("infer: level already zero")
	// ErrVarLimit 表示变量总数达到上限 V。
	ErrVarLimit = errors.New("infer: variable limit exceeded")
	// ErrLevelLimit 表示 Enter 时层级已达 1000。
	ErrLevelLimit = errors.New("infer: level limit exceeded")
	// ErrEnvLimit 表示模式环境已有 1000 个名字。
	ErrEnvLimit = errors.New("infer: environment capacity exceeded")
	// ErrNameExists 表示 Bind 的名字已存在于环境中。
	ErrNameExists = errors.New("infer: name already bound")
	// ErrNameNotFound 表示 Lookup 的名字不存在。
	ErrNameNotFound = errors.New("infer: name not found")
	// ErrBound 表示 Level 查询的变量已绑定到构造子应用。
	ErrBound = errors.New("infer: variable bound to constructor")
	// ErrOccurs 表示 Unify 的出现检查失败。
	ErrOccurs = errors.New("infer: occurs check failed")
	// ErrMismatch 表示 Unify 遇到构造子不匹配。
	ErrMismatch = errors.New("infer: constructor mismatch")
)
