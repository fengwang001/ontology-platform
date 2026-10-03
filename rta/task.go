package rta

// Task is a sporadic/periodic workload with release jitter and a blocking term.
type Task struct {
	ID string
	C  int64
	T  int64
	D  int64
	J  int64
	B  int64
}

// MaxTasks is the fixed capacity of one Analyzer.
const MaxTasks = 32

// maxIDLen is the maximum byte length of a task identifier.
const maxIDLen = 64

func (t Task) valid() error {
	if len(t.ID) == 0 || len(t.ID) > maxIDLen {
		return newError(ReasonInvalidParam, 0, "rta: task id must be non-empty and at most 64 bytes")
	}
	if t.C < 1 || t.C > 1_000_000_000 {
		return newError(ReasonInvalidParam, 0, "rta: C must satisfy 1 <= C <= 1e9")
	}
	if t.T < 1 || t.T > 1_000_000_000 {
		return newError(ReasonInvalidParam, 0, "rta: T must satisfy 1 <= T <= 1e9")
	}
	if t.D < 1 || t.D > t.T {
		return newError(ReasonInvalidParam, 0, "rta: D must satisfy 1 <= D <= T")
	}
	if t.J < 0 || t.J > 1_000_000_000 {
		return newError(ReasonInvalidParam, 0, "rta: J must satisfy 0 <= J <= 1e9")
	}
	if t.B < 0 || t.B > 1_000_000_000 {
		return newError(ReasonInvalidParam, 0, "rta: B must satisfy 0 <= B <= 1e9")
	}
	return nil
}
