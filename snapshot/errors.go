package snapshot

import (
	"errors"
	"fmt"
)

// 可区分的拒绝原因：调用方可用 errors.Is 精确判别。
var (
	// ErrInvalidConfig 配置非法（如变更条数上限为零或为负）。
	ErrInvalidConfig = errors.New("snapshot: invalid config")
	// ErrDuplicateKey 快照内存在重复键。
	ErrDuplicateKey = errors.New("snapshot: duplicate key")
	// ErrNotSorted 快照未按键严格升序。
	ErrNotSorted = errors.New("snapshot: snapshot not sorted")
	// ErrTooManyChanges 变更条数超过配置上限。
	ErrTooManyChanges = errors.New("snapshot: too many changes")
)

// KeyError 携带触发拒绝的具体键、所在侧（"old"/"new"）与序号。
type KeyError struct {
	Kind  error
	Side  string
	Index int
	Key   string
}

func (e *KeyError) Error() string {
	return fmt.Sprintf("%s: side=%s index=%d key=%q", e.Kind, e.Side, e.Index, e.Key)
}

func (e *KeyError) Unwrap() error { return e.Kind }

// ConfigError 携带非法配置的具体原因。
type ConfigError struct {
	Reason string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("%s: %s", ErrInvalidConfig, e.Reason)
}

func (e *ConfigError) Unwrap() error { return ErrInvalidConfig }

// LimitError 携带超限变更条数与上限。
type LimitError struct {
	Count      int
	MaxChanges int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%s: changes=%d max=%d", ErrTooManyChanges, e.Count, e.MaxChanges)
}

func (e *LimitError) Unwrap() error { return ErrTooManyChanges }
