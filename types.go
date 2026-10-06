package parking

import "errors"

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRollback   = errors.New("clock rollback")
	ErrNotFound        = errors.New("spot or plate not found")
	ErrInvalidState    = errors.New("operation not allowed in current state")
	ErrNoSpace         = errors.New("no available spot")
	ErrLease           = errors.New("monthly lease is not active or has expired")
)

type Config struct {
	PublicSpots  int
	MonthlySpots int
	FreeMinutes  int
	BillingUnit  int
	DailyCap     int64
	UnitFee      int64
	OvertimeFee  int64
	GraceDays    int
	EvictionTime int
}

type Lease struct {
	SpotID int
	Plate  string
	Start  int
	End    int
}

type Interval struct {
	StartMinute int
	EndMinute   int
}

type EntryResult struct {
	SpotID     int
	Waiting    bool
	EvictionID string
}

type ExitResult struct {
	SpotID int
	Fee    int64
}

type DecisionLog struct {
	Now      int
	Action   string
	Plate    string
	Reason   string
	Rejected bool
}

func (c Config) valid() bool {
	return c.PublicSpots >= 0 && c.MonthlySpots >= 0 &&
		c.FreeMinutes >= 0 && c.BillingUnit > 0 &&
		c.DailyCap >= 0 && c.UnitFee >= 0 && c.OvertimeFee >= 0 &&
		c.GraceDays >= 0 && c.EvictionTime > 0
}

const minutesPerDay = 24 * 60

type registration struct {
	plate   string
	spot    int
	start   int
	end     int
	version int
	retired bool
}

func leaseActive(reg *registration, now int) bool {
	return reg != nil && now >= reg.start && now < reg.end
}

func withinGrace(reg *registration, now int, graceDays int) bool {
	return reg != nil && now >= reg.end && now < reg.end+graceDays*minutesPerDay
}
