package staffing

import "fmt"

type ErrorCode string

const (
	ErrInvalidArgument           ErrorCode = "invalid_argument"
	ErrClockRolledBack           ErrorCode = "clock_rolled_back"
	ErrNotFound                  ErrorCode = "not_found"
	ErrStatusNotAllowed          ErrorCode = "status_not_allowed"
	ErrPositionFrozen            ErrorCode = "position_frozen"
	ErrHeadcountFull             ErrorCode = "headcount_full"
	ErrSalaryBandWithoutApproval ErrorCode = "salary_band_without_approval"
	ErrCandidateHasPendingOffer  ErrorCode = "candidate_has_pending_offer"
	ErrCoolingDown               ErrorCode = "cooling_down"
	ErrOfferExpired              ErrorCode = "offer_expired"
)

type Error struct {
	Code  ErrorCode
	Index int
	Msg   string
}

func (e *Error) Error() string {
	return string(e.Code) + ": " + e.Msg
}

func errorf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Index: -1, Msg: fmt.Sprintf(format, args...)}
}
