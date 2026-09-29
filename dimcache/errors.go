package dimcache

import "errors"

// 互不相重的拒绝原因。任何一次被拒都不得改变任何状态。
var (
	// ErrEmptyKey 表示查询、读取、回填、更新或删除使用了空键。
	ErrEmptyKey = errors.New("dimcache: key must not be empty")
	// ErrInvalidVersion 表示事件版本号不是正整数。
	ErrInvalidVersion = errors.New("dimcache: event version must be positive")
	// ErrInvalidTombstone 表示墓碑事件携带了值。
	ErrInvalidTombstone = errors.New("dimcache: tombstone event must not carry a value")
	// ErrDeleteMissing 表示删除了源头中不存在（或已是墓碑）的行。
	ErrDeleteMissing = errors.New("dimcache: cannot delete a row that does not exist")
	// ErrUnknownToken 表示回填引用了从未签发的令牌。
	ErrUnknownToken = errors.New("dimcache: unknown read token")
	// ErrTokenUsed 表示回填引用了已经使用过（或已撤销）的令牌。
	ErrTokenUsed = errors.New("dimcache: read token already consumed")
	// ErrTokenStale 表示令牌版本低于当前栅栏，回填必须被拒绝。
	ErrTokenStale = errors.New("dimcache: token version is below the fence")
	// ErrTooManyTrackedKeys 表示跟踪的键数已达上限。
	ErrTooManyTrackedKeys = errors.New("dimcache: tracked key limit exceeded")
)
