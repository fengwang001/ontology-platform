package numa

// Error 描述一次被拒绝操作的可区分原因。
type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrInvalidConfig        Error = "invalid config"
	ErrInvalidArgument      Error = "invalid argument"
	ErrContainerExists      Error = "container already exists"
	ErrContainerNotFound    Error = "container not found"
	ErrReleaseNotFound      Error = "container not found on release"
	ErrQueryNotFound        Error = "container not found on query"
	ErrInsufficientCapacity Error = "insufficient capacity"
	ErrTooManyCombinations  Error = "too many combinations"
	ErrHintNotSatisfied     Error = "hint not satisfied"
)
