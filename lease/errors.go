package lease

import (
	"errors"
	"fmt"
	"time"
)

// 可判定的哨兵错误，均可用 errors.Is 判定。
var (
	// ErrHeld 表示租约仍被他人有效持有，Acquire 被拒绝。
	ErrHeld = errors.New("lease: already held")
	// ErrNotHolder 表示操作者不是当前持有者。
	ErrNotHolder = errors.New("lease: not the holder")
	// ErrNotHeld 表示租约当前无人持有（从未获取、已过期或已释放）。
	ErrNotHeld = errors.New("lease: not held")
)

// HeldError 描述一次因他人持有而被拒的 Acquire，
// 携带当前持有者与剩余时长。
type HeldError struct {
	Holder    string
	Remaining time.Duration
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("lease: held by %q, %s remaining", e.Holder, e.Remaining)
}

// Is 使 errors.Is(err, ErrHeld) 成立。
func (e *HeldError) Is(target error) bool { return target == ErrHeld }
