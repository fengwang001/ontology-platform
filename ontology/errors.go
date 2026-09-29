package ontology

type Reason string

func (r Reason) Error() string {
	return string(r)
}

const (
	ErrInvalidArgument     Reason = "invalid argument"
	ErrTimestampNotAdvanced Reason = "timestamp not advanced"
	ErrNotReady            Reason = "not ready"
	ErrTooOld              Reason = "snapshot timestamp too old"
)
