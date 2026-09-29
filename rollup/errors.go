package rollup

// 拒绝原因均为哨兵错误，彼此互不相同，可用 errors.Is 区分。

var (
	// ErrInvalidIncrement 非法增量：操作类型未知或行标识为空。
	ErrInvalidIncrement = reject("invalid increment: op must be Add/Remove and row id must be non-empty")
	// ErrDuplicateRow 新增的行标识在当前行集中已存在。
	ErrDuplicateRow = reject("invalid increment: row id already exists")
	// ErrRowNotFound 撤回（删除）一条当前不存在的行。
	ErrRowNotFound = reject("invalid increment: cannot remove a row that does not exist")
	// ErrTooManyGroups 新增会使明细组数量超过配置上限。
	ErrTooManyGroups = reject("invalid increment: detail group limit exceeded")
)

type reject string

func (e reject) Error() string { return string(e) }
