package permission

type ErrorCode string

const (
	ErrEffectiveBeforeSubmit ErrorCode = "EFFECTIVE_BEFORE_SUBMIT"
	ErrQueryBeforeHorizon    ErrorCode = "QUERY_BEFORE_HORIZON"
	ErrWithdrawAlreadyActive ErrorCode = "WITHDRAW_ALREADY_ACTIVE"
)

type RuleError struct {
	Code   ErrorCode
	Reason string
}

func (e *RuleError) Error() string {
	return string(e.Code) + ": " + e.Reason
}
