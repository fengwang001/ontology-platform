package routeexecution

import "errors"

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRollback   = errors.New("clock rollback")
	ErrStopNotFound    = errors.New("stop not found")
	ErrInvalidState    = errors.New("invalid stop state")
	ErrInvalidOrder    = errors.New("invalid arrival order")
)

type WindowType uint8

const (
	HardWindow WindowType = iota + 1
	SoftWindow
)

type StopStatus uint8

const (
	StatusOnTime StopStatus = iota + 1
	StatusWaited
	StatusLate
	StatusSkipped
	StatusCanceled
)

type TimeWindow struct {
	LeftSeconds  uint64
	RightSeconds uint64
}

func (w TimeWindow) Contains(at uint64) bool {
	return at >= w.LeftSeconds && at <= w.RightSeconds
}

type Stop struct {
	ID             string
	Window         TimeWindow
	ServiceSeconds uint64
	Type           WindowType
}

type TravelDuration struct {
	From    string
	To      string
	Seconds uint64
	Known   bool
}

type Config struct {
	OriginID                    string
	DepartureSeconds            uint64
	InitialContinuousSeconds    uint64
	MaxContinuousDrivingSeconds uint64
	RestSeconds                 uint64
	DebounceSeconds             uint64
	LockWindowSeconds           uint64
}

type StopResult struct {
	Index               int
	ID                  string
	Status              StopStatus
	ArrivalSeconds      uint64
	ServiceStartSeconds uint64
	DepartureSeconds    uint64
	PublishedETA        uint64
	Reported            bool
	Active              bool
	HasArrival          bool
	HasService          bool
	HasDeparture        bool
	HasPublishedETA     bool
}

type Snapshot struct {
	ClockSeconds uint64
	Stops        []StopResult
}

type OperationKind string

const (
	OperationReportArrival OperationKind = "report_arrival"
	OperationCancelStop    OperationKind = "cancel_stop"
)

type OperationLog struct {
	Kind           OperationKind
	StopID         string
	ArrivalSeconds uint64
	OperatedAt     uint64
	Accepted       bool
	Error          error
	Reason         string
	ClockSeconds   uint64
	Snapshot       Snapshot
}
