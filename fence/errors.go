package fence

import (
	"errors"
	"fmt"
)

// ErrStale 是令牌过旧错误的判定哨兵，用 errors.Is(err, ErrStale) 判定。
var ErrStale = errors.New("fence: stale token")

// StaleError 描述一次被水位拒绝的写入，携带被拒令牌与当前水位。
type StaleError struct {
	Token     Token
	Watermark Token
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("fence: token %d is stale, watermark is %d", e.Token, e.Watermark)
}

// Is 使 errors.Is(err, ErrStale) 成立。
func (e *StaleError) Is(target error) bool { return target == ErrStale }
