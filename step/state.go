package step

// State is the externally observable state of one step.
type State uint8

const (
	Pending      State = 0 // never started
	Running      State = 1 // execution in progress (a start record exists)
	Done         State = 2 // executed successfully
	Failed       State = 3 // execution failed terminally
	Compensating State = 4 // compensation in progress
	Compensated  State = 5 // compensation succeeded
	CompFailed   State = 6 // compensation failed terminally
)

func (s State) String() string {
	switch s {
	case Pending:
		return "pending"
	case Running:
		return "running"
	case Done:
		return "done"
	case Failed:
		return "failed"
	case Compensating:
		return "compensating"
	case Compensated:
		return "compensated"
	case CompFailed:
		return "comp-failed"
	default:
		return "unknown"
	}
}

// Terminal reports whether the state cannot change anymore.
func (s State) Terminal() bool {
	return s == Done || s == Failed || s == Compensated || s == CompFailed
}

// Successful reports whether the step's execution completed successfully.
func (s State) Successful() bool { return s == Done || s == Compensated }

// CompensatedOK reports whether compensation ran and succeeded.
func (s State) CompensatedOK() bool { return s == Compensated }
