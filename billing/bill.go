package billing

// Bill is a single period's charge for a household. Late fees accrue in
// integer "units" of 1/den yuan so daily sub-yuan fractions carry over.
type Bill struct {
	ID        string
	DueDay    int
	Principal int64 // current principal, lowered by an upheld-reduction ruling

	PaidPrincipal int64
	PaidLate      int64
	WaivedLate    int64

	LateUnits      int64 // accrued late fee in units of 1/den
	LastAccrualDay int   // accrual is materialized up to and including this day

	Disputed     bool
	EverDisputed bool // a bill may be disputed only once
	DisputeDay   int
	DisputedDays int // resolved dispute days, excluded from overdue counting

	Stage  int  // dunning stage 0..3, monotone non-decreasing
	Closed bool // fully settled; stage and accrual are frozen

	seq int // creation order, tie-breaks equal due days
}

func (b *Bill) unpaidPrincipal() int64 {
	if d := b.Principal - b.PaidPrincipal; d > 0 {
		return d
	}
	return 0
}

func (b *Bill) accruedLate(den int64) int64 { return b.LateUnits / den }

func (b *Bill) unpaidLate(den int64) int64 {
	return b.accruedLate(den) - b.PaidLate - b.WaivedLate
}

// accrueTo materializes accrual up to and including day. Disputed and closed
// bills do not accrue. Accrual stops once the cap is reached; if a ruling
// lowers the cap below what is already accrued, the excess is kept (no
// clawback) but nothing more accrues.
func (b *Bill) accrueTo(day int, cfg Config) {
	b.LateUnits = b.accruedUnitsAt(day, cfg)
	if day > b.LastAccrualDay {
		b.LastAccrualDay = day
	}
}

// accruedUnitsAt is the pure (non-mutating) view of accrual at day.
func (b *Bill) accruedUnitsAt(day int, cfg Config) int64 {
	if b.Closed || b.Disputed || day <= b.LastAccrualDay {
		return b.LateUnits
	}
	capU := cfg.capUnits(b.Principal)
	if b.LateUnits >= capU {
		return b.LateUnits
	}
	u := b.LateUnits + int64(day-b.LastAccrualDay)*b.unpaidPrincipal()*cfg.rateUnits()
	if u > capU {
		u = capU
	}
	return u
}

// stageAt computes the dunning stage at day now. While disputed the stage is
// frozen at the dispute day; resolved dispute days are excluded from the
// overdue count. The stage never retreats and is frozen once closed.
func (b *Bill) stageAt(now int, th [3]int) int {
	if b.Closed {
		return b.Stage
	}
	eff := now
	if b.Disputed {
		eff = b.DisputeDay
	}
	overdue := eff - b.DueDay - b.DisputedDays
	s := 0
	for i, t := range th {
		if overdue >= t {
			s = i + 1
		}
	}
	if s < b.Stage {
		s = b.Stage
	}
	return s
}
