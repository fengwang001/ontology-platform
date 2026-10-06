package battery

type ErrorKind string

const (
	ErrorInvalidConfig      ErrorKind = "invalid_config"
	ErrorInvalidSample      ErrorKind = "invalid_sample"
	ErrorTimeNotAdvancing   ErrorKind = "time_not_advancing"
	ErrorUnauthorized       ErrorKind = "unauthorized"
	ErrorNotLatched         ErrorKind = "not_latched"
	ErrorNoSampleAfterLatch ErrorKind = "no_sample_after_latch"
	ErrorRecoveryNotMet     ErrorKind = "recovery_not_met"
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string {
	return string(e.Kind) + ": " + e.Message
}

func invalidConfig(message string) error {
	return &Error{Kind: ErrorInvalidConfig, Message: message}
}

func invalidSample(message string) error {
	return &Error{Kind: ErrorInvalidSample, Message: message}
}
