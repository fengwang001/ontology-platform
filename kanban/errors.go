package kanban

type Error string

const (
	ErrInvalidArgument    Error = "invalid argument"
	ErrClockRewound       Error = "clock rewound"
	ErrCardNotFound       Error = "card not found"
	ErrVersionConflict    Error = "version conflict"
	ErrIllegalTransition  Error = "illegal transition"
	ErrDependencyBlocked  Error = "dependency blocked"
	ErrDependencyExists   Error = "dependency already exists"
	ErrDependencyCycle    Error = "dependency cycle"
	ErrExpediteOccupied   Error = "expedite occupied"
	ErrColumnLimitFull    Error = "column limit full"
	ErrAssigneeLimitFull  Error = "assignee limit full"
	ErrDependencyNotFound Error = "dependency not found"
)

func (e Error) Error() string { return string(e) }
