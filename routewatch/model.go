package routewatch

type StopKind int

const (
	HardWindow StopKind = iota
	SoftWindow
)

type Window struct {
	Earliest int64
	Latest   int64
}

type Stop struct {
	ID       string
	Kind     StopKind
	Window   Window
	Service  int64
	Canceled bool
}

type TravelTable struct {
	DepotID  string
	Duration map[[2]string]int64
}

type Config struct {
	RouteID         string
	DepotID         string
	Departure       int64
	Stops           []Stop
	Travel          TravelTable
	MaxDriving      int64
	RestDuration    int64
	DebounceSeconds int64
	LockWindow      int64
}

type Status int

const (
	StatusOnTime Status = iota
	StatusWaited
	StatusLate
	StatusSkipped
)

func (s Status) String() string { return statusName[s] }

var statusName = [...]string{
	StatusOnTime:  "on_time",
	StatusWaited:  "waited_then_on_time",
	StatusLate:    "late",
	StatusSkipped: "skipped",
}

type StopResult struct {
	Index        int
	ID           string
	Arrival      int64
	ServiceStart int64
	Departure    int64
	Driving      int64
	Status       Status
	Canceled     bool
	Valid        bool
	FromIndex    int
}

type PublishedETA struct {
	Index int
	ID    string
	ETA   int64
}

type Snapshot struct {
	RouteID   string
	Clock     int64
	Results   []StopResult
	Published []PublishedETA
}

// validate checks the static route configuration. It is the single source of
// truth for the "invalid parameter" rejection used by New.
func (c Config) validate() error {
	if c.RouteID == "" || c.DepotID == "" {
		return ErrInvalidParam
	}
	if c.Departure < 0 || c.MaxDriving < 0 || c.RestDuration < 0 ||
		c.DebounceSeconds < 0 || c.LockWindow < 0 {
		return ErrInvalidParam
	}
	if len(c.Stops) == 0 {
		return ErrInvalidParam
	}
	if c.Travel.DepotID != "" && c.Travel.DepotID != c.DepotID {
		return ErrInvalidParam
	}
	seen := map[string]bool{c.DepotID: true}
	for i := range c.Stops {
		s := &c.Stops[i]
		if s.ID == "" || seen[s.ID] {
			return ErrInvalidParam
		}
		seen[s.ID] = true
		if s.Kind != HardWindow && s.Kind != SoftWindow {
			return ErrInvalidParam
		}
		if s.Window.Earliest < 0 || s.Window.Latest < s.Window.Earliest || s.Service < 0 {
			return ErrInvalidParam
		}
	}
	if c.Travel.Duration == nil {
		return ErrInvalidParam
	}
	// The travel table only needs to contain pairs that may actually be
	// driven; absent pairs are treated as zero seconds. Negative values are
	// illegal.
	for _, d := range c.Travel.Duration {
		if d < 0 {
			return ErrInvalidParam
		}
	}
	return nil
}

func absDelta(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}
