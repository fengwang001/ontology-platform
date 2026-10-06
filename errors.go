package thinpool

type ErrorCode string

const (
	ErrInvalidArgument ErrorCode = "invalid_argument"
	ErrNotFound        ErrorCode = "volume_not_found"
	ErrExists          ErrorCode = "volume_already_exists"
	ErrOvercommit      ErrorCode = "overcommit_limit_exceeded"
	ErrReservePool     ErrorCode = "reservation_exceeds_pool"
	ErrReserveVolume   ErrorCode = "reservation_exceeds_volume"
	ErrVolumeHasData   ErrorCode = "volume_still_has_data"
	ErrInsufficient    ErrorCode = "insufficient_reservation"
	ErrPoolExhausted   ErrorCode = "pool_exhausted"
)

type Error struct {
	Code ErrorCode
	msg  string
}

func (e Error) Error() string {
	return e.msg
}

func thinError(code ErrorCode, msg string) Error {
	return Error{Code: code, msg: msg}
}
