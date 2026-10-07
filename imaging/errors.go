package imaging

type ErrorCode string

const (
	ErrInvalidParameter    ErrorCode = "invalid_parameter"
	ErrClockRollback       ErrorCode = "clock_rollback"
	ErrNotFound            ErrorCode = "not_found"
	ErrInvalidState        ErrorCode = "invalid_state"
	ErrDeviceClassMismatch ErrorCode = "device_class_mismatch"
	ErrImplantIncompatible ErrorCode = "implant_incompatible"
	ErrRenalMissing        ErrorCode = "renal_result_missing_or_expired"
	ErrRenalInsufficient   ErrorCode = "renal_insufficient"
	ErrDeviceConflict      ErrorCode = "device_interval_conflict"
	ErrQCConflict          ErrorCode = "quality_control_conflict"
	ErrObservationFull     ErrorCode = "observation_capacity_exceeded"
)

type DomainError struct {
	Code   ErrorCode
	Reason string
}

func (e *DomainError) Error() string {
	return string(e.Code) + ": " + e.Reason
}

func failure(code ErrorCode, reason string) *DomainError {
	return &DomainError{Code: code, Reason: reason}
}

func asDomainError(err error) *DomainError {
	if err == nil {
		return nil
	}
	if domainErr, ok := err.(*DomainError); ok {
		return domainErr
	}
	return nil
}
