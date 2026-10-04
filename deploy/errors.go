package deploy

import "errors"

// 可经 errors.Is 区分的拒绝原因。拒绝次序固定为：
// 参数非法 > 时钟回退 > 无权限 > 环境或编号不存在 > 非属主 >
// 状态不符 > 版本过时 > 未知版本 > 自批 > 重复批准。
var (
	ErrInvalidArgument   = errors.New("deploy: invalid argument")
	ErrClockBackwards    = errors.New("deploy: clock moved backwards")
	ErrPermission        = errors.New("deploy: permission denied")
	ErrNotFound          = errors.New("deploy: environment or request not found")
	ErrNotOwner          = errors.New("deploy: caller is not the request owner")
	ErrInvalidState      = errors.New("deploy: request is not in the required state")
	ErrVersionStale      = errors.New("deploy: version is stale for current deployment")
	ErrUnknownVersion    = errors.New("deploy: rollback version was never successfully deployed")
	ErrSelfApproval      = errors.New("deploy: self approval is forbidden")
	ErrDuplicateApproval = errors.New("deploy: duplicate active approval")
)
