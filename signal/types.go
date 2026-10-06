package signal

type Phase struct {
	MinGreen int
	MaxGreen int
	Clear    int
}

type Plan struct {
	ID        string
	Greens    []int
	Offset    int64
	MaxAdjust int64
}

type BusMode int

const (
	BusExtend BusMode = iota + 1
	BusShorten
)

type EmStatus string

const (
	EmQueued    EmStatus = "Queued"
	EmActive    EmStatus = "Active"
	EmCompleted EmStatus = "Completed"
)

type BusStatus string

const (
	BusCompleted BusStatus = "Completed"
	BusPreempted BusStatus = "Preempted"
)

type PlanStatus string

const (
	PlanActive   PlanStatus = "Active"
	PlanPending  PlanStatus = "Pending"
	PlanReplaced PlanStatus = "Replaced"
)

type QueryResult struct {
	Time        int64
	Phase       int
	Elapsed     int64
	Remaining   int64
	InClearance bool
	Deviation   int64
	Cycle       int64
}
