package resolver

// ErrorKind classifies the reason an operation was rejected.
type ErrorKind int

const (
	// ErrInvalidParam indicates an illegal argument: an empty or overlong
	// name, an out-of-range argument count or variable index, a non-ground
	// or too-deep ground type, a context variable missing from the head,
	// or an out-of-range depth limit.
	ErrInvalidParam ErrorKind = iota
	// ErrDuplicateInstance indicates an instance whose head equals an
	// existing same-trait instance head up to variable renaming.
	ErrDuplicateInstance
	// ErrInstanceLimit indicates that the instance count limit was reached.
	ErrInstanceLimit
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "invalid parameter"
	case ErrDuplicateInstance:
		return "duplicate instance"
	case ErrInstanceLimit:
		return "instance limit exceeded"
	}
	return "unknown error"
}

// Error is a rejection of an operation. Rejected operations never change
// any state.
type Error struct {
	Kind   ErrorKind
	Detail string
}

func (e *Error) Error() string {
	return e.Kind.String() + ": " + e.Detail
}

// FailKind is the category of a resolution failure.
type FailKind int

const (
	// FailNoInstance: no candidate instance matched the goal.
	FailNoInstance FailKind = iota
	// FailAmbiguous: candidates exist but none is uniquely most specific.
	FailAmbiguous
	// FailDepthExceeded: the goal's level exceeded the depth limit.
	FailDepthExceeded
	// FailCycle: the goal already occurs among its ancestor goals.
	FailCycle
)

func (k FailKind) String() string {
	switch k {
	case FailNoInstance:
		return "no instance"
	case FailAmbiguous:
		return "ambiguous"
	case FailDepthExceeded:
		return "depth exceeded"
	case FailCycle:
		return "cycle"
	}
	return "unknown failure"
}

// Failure describes a failed resolution: the category plus the goal
// (trait, canonical type text) at which the failure originated. It is the
// first failure in depth-first, context-declaration order.
type Failure struct {
	Kind  FailKind
	Trait string
	Type  string
}

func (f *Failure) String() string {
	return f.Kind.String() + " at " + f.Trait + "<" + f.Type + ">"
}
