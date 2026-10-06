package reservation

type Interval struct {
	Start int
	End   int
}

// Valid reports whether iv is a left-closed right-open nonempty interval.
func (iv Interval) Valid() bool { return iv.Start < iv.End }

// Contains reports whether integer time t is inside iv, i.e. Start <= t < End.
func (iv Interval) Contains(t int) bool { return iv.Start <= t && t < iv.End }

type ReservationState int

const (
	StateHolding ReservationState = iota
	StateConfirmed
	StateVoid
	StateCancelled
	StateCompleted
	StateReleased
)

var stateNames = [...]string{
	StateHolding:   "占位中",
	StateConfirmed: "已确认",
	StateVoid:      "已失效",
	StateCancelled: "已取消",
	StateCompleted: "已完成",
	StateReleased:  "已释放",
}

func (s ReservationState) String() string {
	if int(s) < 0 || int(s) >= len(stateNames) {
		return "未知状态"
	}
	return stateNames[s]
}

type Reservation struct {
	ID        int64
	Feeder    int
	Start     int
	End       int
	Power     int
	State     ReservationState
	CreatedAt int
	ExpiresAt int
}

func (r *Reservation) Interval() Interval { return Interval{r.Start, r.End} }

// occupiesAt reports whether the reservation still contributes occupancy at now.
// A confirmed reservation whose end is not after now has completed and holds nothing.
func (r *Reservation) occupiesAt(now int) bool {
	switch r.State {
	case StateHolding:
		return true
	case StateConfirmed:
		return r.End > now
	default:
		return false
	}
}

type Logger interface {
	Logf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Logf(string, ...any) {}

type Config struct {
	HoldDuration int
	Log          Logger
}
