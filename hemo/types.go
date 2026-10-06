package hemo

import "fmt"

// Time bounds mandated by the specification. Time is an integer number of
// minutes; legal timestamps lie in [0, MaxTime].
const (
	MinTime       = 0
	MaxTime       = 10_000_000
	MinutesPerDay = 1440
)

// Infection is the infection state recorded for a patient.
type Infection uint8

const (
	InfectionUnknown  Infection = iota // 状态待定
	InfectionNegative                  // 阴性
	InfectionHBV                       // 乙肝阳性
	InfectionHCV                       // 丙肝阳性
)

// Valid reports whether v is one of the four defined infection states.
func (v Infection) Valid() bool {
	return v >= InfectionUnknown && v <= InfectionHCV
}

func (v Infection) String() string {
	switch v {
	case InfectionUnknown:
		return "unknown"
	case InfectionNegative:
		return "negative"
	case InfectionHBV:
		return "hbv"
	case InfectionHCV:
		return "hcv"
	default:
		return fmt.Sprintf("infection(%d)", int(v))
	}
}

// Zone is the area a chair belongs to.
type Zone uint8

const (
	ZoneNormal    Zone = iota // 普通区
	ZoneIsolation             // 隔离区
)

func (z Zone) String() string {
	switch z {
	case ZoneNormal:
		return "normal"
	case ZoneIsolation:
		return "isolation"
	default:
		return fmt.Sprintf("zone(%d)", int(z))
	}
}

// Chair is a registered treatment station.
type Chair struct {
	ID          string
	Zone        Zone
	Observation bool // meaningful only in the normal zone (观察位)

	// Faults is the list of mutually disjoint downtime windows (sorted by
	// start). A chair is unavailable at t iff some window [From,To) contains
	// t. Windows entirely in the past are retained as history.
	Faults []FaultWindow
}

// AvailableAt reports whether the chair can accept a treatment starting at t.
func (c *Chair) AvailableAt(t int) bool {
	for i := range c.Faults {
		f := &c.Faults[i]
		if f.From <= t && t < f.To {
			return false
		}
	}
	return true
}

// FaultWindow is one downtime interval [From, To).
type FaultWindow struct {
	From int
	To   int
}

// faultAt returns the window containing t, or nil.
func (c *Chair) faultAt(t int) *FaultWindow {
	for i := range c.Faults {
		f := &c.Faults[i]
		if f.From <= t && t < f.To {
			return f
		}
	}
	return nil
}

// Patient is a registered patient.
type Patient struct {
	ID        string
	Infection Infection
}

// Treatment is one chair occupation: chair is busy on [Start, End) and the
// following disinfection makes it busy until End+gap, where gap is derived
// from InfectionAtStart of this treatment and the next treatment on the
// same chair.
type Treatment struct {
	ID         string
	PatientID  string
	ChairID    string
	Start      int
	End        int
	Duration   int
	PlanID     string // empty for a relocated treatment without a plan link
	Occurrence int    // index within Plan, -1 when not plan-derived

	// InfectionAtStart records the patient infection state used when this
	// treatment was (re)scheduled. It determines the disinfection tail left
	// to the next treatment on the chair.
	InfectionAtStart Infection

	// Cancelled treatments are removed from all indexes immediately, so the
	// registry only ever contains live treatments; the flag is kept for
	// completeness of future audit extensions.
	Cancelled bool
}

// Plan is a weekly recurring treatment scheme valid on [ValidFrom, ValidTo].
type Plan struct {
	ID        string
	PatientID string
	// Weekdays is a subset of {0..6}; day 0 is any day t with t/1440 % 7 == 0
	// (weeks start at integer multiples of 7 days from time 0).
	Weekdays  [7]bool
	DayStart  int // minutes within the day, 0 <= DayStart < 1440
	Duration  int
	ValidFrom int
	ValidTo   int

	// TreatmentIDs lists the occurrences created for the plan, ordered by
	// ascending start. Cancelled single occurrences are replaced by "".
	TreatmentIDs []string
	Cancelled    bool
}

// Config holds the durations (minutes) of the disinfection/ recovery rules.
type Config struct {
	// Regular disinfection after a treatment of the given infection type:
	// minimum gap between end of one treatment and start of the next on the
	// same chair when infection types match (or both are negative/pending).
	RegularNegative int
	RegularHBV      int
	RegularHCV      int
	RegularUnknown  int
	// DeepDisinfect is the minimum end-to-start gap required when an HBV and
	// an HCV treatment are adjacent on the same isolation-zone chair.
	DeepDisinfect int
	// MinRecovery is the minimum gap between two treatments of the same
	// patient (end of earlier to start of later).
	MinRecovery int
}

// RegularGap returns the same-type disinfection tail for infection state v.
func (c Config) RegularGap(v Infection) int {
	switch v {
	case InfectionNegative:
		return c.RegularNegative
	case InfectionHBV:
		return c.RegularHBV
	case InfectionHCV:
		return c.RegularHCV
	default:
		return c.RegularUnknown
	}
}
