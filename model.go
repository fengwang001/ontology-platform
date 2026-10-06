package parking

import "errors"
import "sync"

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRolledBack = errors.New("clock rolled back")
	ErrNotFound        = errors.New("spot or plate does not exist")
	ErrInvalidState    = errors.New("operation is not allowed in current state")
	ErrNoSpace         = errors.New("no available spot")
	ErrLease           = errors.New("monthly lease has not started or has expired")
)

const Day = 1440

type Config struct {
	VacateMinutes int
	FreeMinutes   int
	BillingUnit   int
	UnitFee       int
	DailyCap      int
	OverTimeRate  int
	GraceDays     int
}

type Interval struct {
	Start int
	End   int
}

type Registry map[string]Lease

type Lease struct {
	Plate    string
	Spot     int
	Start    int
	End      int
	GraceTo  int
	Share    Interval
	HasShare bool
}

type Occupancy struct {
	Plate           string
	Spot            int
	Entry           int
	MonthlyAtEntry  bool
	Waiting         bool
	VacateDeadline  int
	HasVacate       bool
	ShareEndAtEntry int
}

type ExitResult struct {
	Spot     int
	Fee      int
	BaseFee  int
	Overtime int
}

type spotState struct {
	ID         int
	Occupied   string
	Owner      string
	Public     bool
	Generation int
}

type vehicleState struct {
	Plate           string
	MonthlyAtEntry  bool
	Entry           int
	Spot            int
	Waiting         bool
	VacateStart     int
	VacateDeadline  int
	HasVacate       bool
	VacateEnd       int
	ShareEndAtEntry int
	ShareAtEntry    Interval
	HasShareAtEntry bool
	EnteredShared   bool
}

type scheduledEvent struct {
	at         int
	kind       byte
	spot       int
	generation int
}

const (
	eventShareEnd byte = iota + 1
	eventShareStart
	eventLeaseStart
	eventLeaseEnd
	eventGraceEnd
)

type Manager struct {
	mu         sync.Mutex
	lastNow    int
	cfg        Config
	spots      []*spotState
	leases     map[string]*Lease
	vehicles   map[string]*vehicleState
	publicFree *minIDSet
	sharedFree *minIDSet
	events     *eventHeap
	waiting    []string
}
