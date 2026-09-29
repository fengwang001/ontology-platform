package featureflag

import "fmt"

// PublishErrorCode 区分一次发布被拒绝的具体原因。
type PublishErrorCode string

const (
	// ErrVariantNotFound 规则或放量引用了未声明的变体，或前置要求的变体不存在。
	ErrVariantNotFound PublishErrorCode = "variant_not_found"
	// ErrInvalidWeights 权重为负，或某放量变体权重之和不等于 10000。
	ErrInvalidWeights PublishErrorCode = "invalid_weights"
	// ErrPrerequisiteNotFound 前置开关在本次规则集中不存在。
	ErrPrerequisiteNotFound PublishErrorCode = "prerequisite_not_found"
	// ErrPrerequisiteCycle 前置依赖构成环。
	ErrPrerequisiteCycle PublishErrorCode = "prerequisite_cycle"
)

// PublishError 描述一次发布被拒绝的原因。同一时刻可能有多个问题，
// 但 Publish 只按规定的优先级返回第一个错误。
type PublishError struct {
	Code PublishErrorCode
	Flag string
	Msg  string
}

func (e *PublishError) Error() string {
	if e.Flag == "" {
		return fmt.Sprintf("featureflag: publish rejected (%s): %s", e.Code, e.Msg)
	}
	return fmt.Sprintf("featureflag: publish rejected (%s) on flag %q: %s", e.Code, e.Flag, e.Msg)
}

func pubErr(code PublishErrorCode, flag, format string, args ...any) *PublishError {
	return &PublishError{Code: code, Flag: flag, Msg: fmt.Sprintf(format, args...)}
}
