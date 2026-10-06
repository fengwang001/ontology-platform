package qc

type ErrorCode string

const (
	ErrInvalidParameter  ErrorCode = "invalid_parameter"
	ErrClockRewound      ErrorCode = "clock_rewound"
	ErrNotFound          ErrorCode = "not_found"
	ErrAssayOutOfControl ErrorCode = "assay_out_of_control"
	ErrNeverControlled   ErrorCode = "never_controlled"
	ErrQCExpired         ErrorCode = "qc_expired"
	ErrStatusMismatch    ErrorCode = "status_mismatch"
)

type Error struct {
	Code    ErrorCode
	Message string
}

func (e Error) Error() string {
	return string(e.Code) + ": " + e.Message
}

func errorf(code ErrorCode, message string) error {
	return Error{Code: code, Message: message}
}
