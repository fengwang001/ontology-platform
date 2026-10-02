package syncookie

// Error is the distinguishable failure reason reported by the validator.
type Error string

const (
	ErrInvalidParam  Error = "invalid construction parameter"
	ErrInvalidTime   Error = "now out of range"
	ErrClockBack     Error = "clock moved backwards"
	ErrInvalidMSS    Error = "mss out of range"
	ErrAcceptFull    Error = "accept queue full"
	ErrEmpty         Error = "accept queue empty"
	ErrBadAck        Error = "ack/seq does not match half-open entry"
	ErrCookie        Error = "invalid cookie"
	ErrCookieExpired Error = "cookie expired"
)

func (e Error) Error() string { return string(e) }
