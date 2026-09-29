package dimcache

import "errors"

// 非法输入 / 回填被拒的错误类别。每一类对应互不相同、可区分的拒绝原因。
var (
	// ErrEmptyKey: 键为空字符串。
	ErrEmptyKey = errors.New("dimcache: empty key")
	// ErrTokenUnknown: 令牌不存在（伪造或来源不明）。
	ErrTokenUnknown = errors.New("dimcache: unknown read token")
	// ErrTokenUsed: 令牌已被使用过，令牌一次性有效。
	ErrTokenUsed = errors.New("dimcache: read token already consumed")
	// ErrDeleteMissing: 删除一个源头中不存在（且无墓碑）的键。
	ErrDeleteMissing = errors.New("dimcache: delete on missing key")
	// ErrTrackedKeysExceeded: 被跟踪的不同键数量超过上限。
	ErrTrackedKeysExceeded = errors.New("dimcache: tracked key limit exceeded")
	// ErrTokenStale: 回填时令牌版本低于栅栏要求的回填下限。
	ErrTokenStale = errors.New("dimcache: token version below backfill floor")
)
