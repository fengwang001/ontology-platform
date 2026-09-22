// Package step 定义 SAGA 中单个步骤：正向动作、补偿动作、重试策略与幂等键。
// 本包不依赖工程内任何其他包。
package step

import "errors"

// Action 是正向或补偿动作。返回 nil 表示明确成功；返回 OutcomeError(Unknown)
// 表示「超时/未知」（效果未知）；其他 error 表示明确失败（确定无效果）。
type Action func() error

// Step 描述一个有序的 SAGA 步骤。Forward 为 nil 时视为空动作（立即成功）；
// Compensate 为 nil 表示该步骤无需补偿（但成功后进入补偿阶段会得到可判定错误）。
type Step struct {
	ID         string
	Key        string // 幂等键，同一 SAGA 内必须唯一
	Forward    Action
	Compensate Action
	Retryable  bool
}

// Kind 描述一次动作结果的确定程度。
type Kind int

const (
	// Definite 表示明确失败：调用确定没有产生任何效果。
	Definite Kind = iota
	// Unknown 表示超时/未知：动作是否已生效无法确定。
	Unknown
)

// OutcomeError 把错误区分为「明确失败」与「未知」。未知错误按推定成功处理。
type OutcomeError struct {
	Kind Kind
	Err  error
}

func (e *OutcomeError) Error() string {
	if e == nil {
		return ""
	}
	if e.Kind == Unknown {
		return "saga step: unknown outcome: " + e.Err.Error()
	}
	return "saga step: definite failure: " + e.Err.Error()
}

func (e *OutcomeError) Unwrap() error { return e.Err }

// NewDefiniteFailure 构造一个明确失败（确定无效果，可安全重试/不补偿）。
func NewDefiniteFailure(err error) error {
	if err == nil {
		return nil
	}
	return &OutcomeError{Kind: Definite, Err: err}
}

// NewUnknownOutcome 构造一个超时/未知错误（可能已生效，按推定成功处理）。
func NewUnknownOutcome(err error) error {
	if err == nil {
		return nil
	}
	return &OutcomeError{Kind: Unknown, Err: err}
}

// IsUnknown 报告错误是否为「未知结果」。
func IsUnknown(err error) bool {
	var oe *OutcomeError
	return errors.As(err, &oe) && oe.Kind == Unknown
}
