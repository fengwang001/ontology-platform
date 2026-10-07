package compensate

// OpKind enumerates the three kinds of ordered side-effect sub-operations.
type OpKind int

const (
	OpSetAttrs OpKind = iota + 1
	OpCreateLink
	OpDeleteLink
	OpValidate
)

// SubOp is one declared side-effect sub-operation of an action.
type SubOp struct {
	Kind     OpKind
	Object   InstanceID
	Attrs    Attrs
	Link     Link
	HookName string
}

// Action is an ordered list of sub-operations executed declaration order.
type Action struct {
	Name   string
	Ops    []SubOp
	Inject *FaultPlan
}

// FaultPoint identifies where an injected failure/exception occurs.
type FaultPoint int

const (
	// FaultNone means no injection.
	FaultNone FaultPoint = iota
	// FaultApplyFail: the forward op returns a business failure.
	FaultApplyFail
	// FaultApplyPanic: the forward op raises a runtime panic.
	FaultApplyPanic
	// FaultUndoFail: the registered inverse returns a failure.
	FaultUndoFail
	// FaultUndoPanic: the registered inverse raises a runtime panic.
	FaultUndoPanic
)

// FaultPlan injects failures/exceptions at chosen op indices (tests only).
type FaultPlan struct {
	Apply map[int]FaultPoint
	Undo  map[int]FaultPoint
}

// CompFailure is one compensation failure record.
type CompFailure struct {
	OpIndex int
	Class   ReasonClass
	Detail  string
}

// ActionResult is the final verdict of executing one action.
type ActionResult struct {
	ActionName    string
	Committed     bool
	Primary       ReasonClass
	Reject        *Failure
	FailedOpIndex int
	CompFailures  []CompFailure
}

// involvedInstances returns the object instances an op observes or mutates.
func (op SubOp) involvedInstances() []InstanceID {
	switch op.Kind {
	case OpSetAttrs, OpValidate:
		return []InstanceID{op.Object}
	case OpCreateLink, OpDeleteLink:
		return []InstanceID{op.Link.Source, op.Link.Target}
	default:
		return nil
	}
}

// resources returns the lock tokens guarding the op's mutable state.
func (op SubOp) resources() []string {
	switch op.Kind {
	case OpSetAttrs, OpValidate:
		return []string{tokenObj(op.Object)}
	case OpCreateLink, OpDeleteLink:
		return []string{tokenLink(op.Link.ID)}
	default:
		return nil
	}
}
