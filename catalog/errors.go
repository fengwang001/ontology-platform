package catalog

import "errors"

// 三类统计异常，均可被 errors.Is 直接区分。
var (
	// ErrMissingStats：谓词引用的列没有统计（估计回退默认选择率，不中止选择）。
	ErrMissingStats = errors.New("missing column statistics")
	// ErrStaleStats：统计行数与目录行数不一致超过阈值（按目录行数校正）。
	ErrStaleStats = errors.New("stale table statistics")
	// ErrCorruptStats：直方图桶计数之和不符或边界非递增（可判定错误，中止选择）。
	ErrCorruptStats = errors.New("corrupt statistics")
	// ErrSelfJoin：谓词两侧为同一张表，当前子集模型明确拒绝。
	ErrSelfJoin = errors.New("self join is not supported")
)

// StaleFraction 是过期判定的相对阈值：|stat-cat|/cat > 10% 即过期。
const StaleFraction = 0.1
