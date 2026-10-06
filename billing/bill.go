package billing

// Bill is a single property-fee bill. Late fees are accrued lazily: the
// integer late fee is materialized up to accruedThrough, and the
// sub-unit fractional remainder is carried in frac (denominator
// Config.RateDen) so nothing is ever discarded.
type Bill struct {
	seq    int64 // global generation sequence, tie-breaks equal due days
	id     string
	period string
	dueDay int64

	principal      int64 // current principal (reduced by a ruling)
	principalPaid  int64
	lateFee        int64 // accrued integer late fee
	lateFeePaid    int64
	lateFeeWaived  int64
	frac           int64 // carried fraction numerator, 0 <= frac < RateDen
	accruedThrough int64 // last day the late fee has been accrued through

	stage  int
	closed bool

	disputed     bool
	disputeStart int64
	disputedDays int64 // resolved dispute days, excluded from overdue count
	everDisputed bool
	ruled        bool
}

func (b *Bill) unpaidPrincipal() int64 { return b.principal - b.principalPaid }

func (b *Bill) unpaidLateFee() int64 { return b.lateFee - b.lateFeePaid - b.lateFeeWaived }

func lateFeeCap(cfg Config, principal int64) int64 {
	return principal * cfg.CapNum / cfg.CapDen
}

// accrueTo materializes the late fee through day t. It is a no-op for
// closed or disputed bills and never moves accruedThrough backwards.
// The unpaid principal is constant over any single accrual window
// because every operation that changes it accrues first.
func (b *Bill) accrueTo(cfg Config, t int64) {
	if b.closed || b.disputed || t <= b.accruedThrough {
		return
	}
	n := t - b.accruedThrough
	b.accruedThrough = t
	unpaid := b.unpaidPrincipal()
	if unpaid <= 0 || cfg.RateNum == 0 {
		return
	}
	raw := b.frac + n*unpaid*cfg.RateNum
	add := raw / cfg.RateDen
	b.frac = raw % cfg.RateDen
	cap := lateFeeCap(cfg, b.principal)
	if room := cap - b.lateFee; add >= room {
		if room > 0 {
			b.lateFee += room
		}
		b.frac = 0 // capped: stop growing, drop the remainder
		return
	}
	b.lateFee += add
}

// projectedLateFee is the pure (non-mutating) counterpart of accrueTo:
// the integer late fee the bill would show at day t.
func (b *Bill) projectedLateFee(cfg Config, t int64) int64 {
	if b.closed || b.disputed || t <= b.accruedThrough {
		return b.lateFee
	}
	unpaid := b.unpaidPrincipal()
	if unpaid <= 0 || cfg.RateNum == 0 {
		return b.lateFee
	}
	raw := b.frac + (t-b.accruedThrough)*unpaid*cfg.RateNum
	add := raw / cfg.RateDen
	cap := lateFeeCap(cfg, b.principal)
	if cap < b.lateFee {
		cap = b.lateFee // a ruling may shrink the cap below the accrued fee
	}
	if b.lateFee+add > cap {
		return cap
	}
	return b.lateFee + add
}

// overdueDays counts days past the due day, excluding resolved dispute
// days. Never negative.
func (b *Bill) overdueDays(now int64) int64 {
	d := now - b.dueDay - b.disputedDays
	if d < 0 {
		return 0
	}
	return d
}

// advanceStage moves the dunning stage forward according to the overdue
// days at now. Frozen for closed and disputed bills; never regresses.
func (b *Bill) advanceStage(cfg Config, now int64) {
	if b.closed || b.disputed {
		return
	}
	overdue := b.overdueDays(now)
	stage := StageNone
	for i, th := range cfg.StageThresholds {
		if overdue >= th {
			stage = i + 1
		}
	}
	if stage > b.stage {
		b.stage = stage
	}
}
