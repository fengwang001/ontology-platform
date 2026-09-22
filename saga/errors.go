package saga

import "errors"

// 可判定的哨兵错误：定义校验类
var (
	ErrNoSteps       = errors.New("saga: step list is empty")
	ErrDuplicateKey  = errors.New("saga: duplicate idempotency key")
	ErrNilCompensate = errors.New("saga: succeeded step has nil compensate action")
	ErrMaxSteps      = errors.New("saga: step count exceeds MaxSteps")
	ErrMaxRetries    = errors.New("saga: invalid MaxRetries")
	ErrJournalLimit  = errors.New("saga: journal record limit exceeded")
)

// 可判定的实例生命周期错误
var (
	// ErrInstanceNotFound：对不存在的实例 Resume/查询。
	ErrInstanceNotFound = errors.New("saga: instance not found")
	// ErrInstanceRunning：实例正在运行时并发 Resume。
	ErrInstanceRunning = errors.New("saga: instance is running")
)

// LimitError 携带上下文的资源上限错误（保留哨兵比较能力）。
type LimitError struct{ Kind error }

func (e *LimitError) Error() string { return e.Kind.Error() }
func (e *LimitError) Unwrap() error { return e.Kind }
