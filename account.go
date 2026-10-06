package mileage

type segmentRecord struct {
	qualifying int
	redeemable int
	flownAt    int64
	period     int64
	posted     bool
}

type redemptionRecord struct {
	cost     int
	at       int64
	canceled bool
}

type account struct {
	id               string
	openedAt         int64
	lastClockAt      int64
	lastActivityAt   int64
	frozen           bool
	level            int
	currentPeriod    int64
	periodQualifying map[int64]int
	redeemable       int
	debt             int
	segments         map[string]*segmentRecord
	redemptions      map[string]*redemptionRecord
}

func levelForMiles(thresholds [3]int, miles int) int {
	if miles >= thresholds[2] {
		return 3
	}
	if miles >= thresholds[1] {
		return 2
	}
	if miles >= thresholds[0] {
		return 1
	}
	return 0
}

func ceilDiv(numerator, denominator int) int {
	return (numerator + denominator - 1) / denominator
}

func newAccount(id string, now int64) *account {
	return &account{
		id:               id,
		openedAt:         now,
		lastClockAt:      now,
		lastActivityAt:   now,
		currentPeriod:    0,
		periodQualifying: make(map[int64]int),
		segments:         make(map[string]*segmentRecord),
		redemptions:      make(map[string]*redemptionRecord),
	}
}

func (a *account) periodAt(config Config, at int64) (int64, bool) {
	if at < a.openedAt {
		return 0, false
	}
	return (at - a.openedAt) / config.PeriodSeconds, true
}

func (a *account) rollForward(config Config, now int64) {
	wantPeriod := (now - a.openedAt) / config.PeriodSeconds
	if wantPeriod <= a.currentPeriod {
		return
	}
	for a.currentPeriod < wantPeriod {
		periodLevel := levelForMiles(config.Thresholds, a.periodQualifying[a.currentPeriod])
		floor := a.level - 1
		if floor < 0 {
			floor = 0
		}
		if periodLevel < floor {
			a.level = floor
		} else {
			a.level = periodLevel
		}
		a.currentPeriod++
	}
}

func (a *account) frozenAt(config Config, now int64) bool {
	return a.frozen || now-a.lastActivityAt >= config.InactivitySeconds
}

func (a *account) applyRedeemable(gross int) (debtRepaid, added int) {
	if gross >= a.debt {
		debtRepaid = a.debt
		added = gross - a.debt
		a.debt = 0
		a.redeemable += added
		return debtRepaid, added
	}
	a.debt -= gross
	return gross, 0
}

func (a *account) deductRedeemable(amount int) int {
	if a.redeemable >= amount {
		a.redeemable -= amount
		return 0
	}
	debt := amount - a.redeemable
	a.redeemable = 0
	a.debt += debt
	return debt
}

func (a *account) credit(config Config, in CreditInput, segmentPeriod int64) CreditResult {
	base := in.Segment.Distance * in.Segment.FarePercent / 100
	if rounded := ceilDiv(in.Segment.Distance*in.Segment.FarePercent, 100); rounded > base {
		base = rounded
	}
	if base < config.MinimumBaseMiles {
		base = config.MinimumBaseMiles
	}

	levelBefore := a.level
	gross := base + base*config.BonusPercent[levelBefore]/100
	debtRepaid, added := a.applyRedeemable(gross)
	a.periodQualifying[segmentPeriod] += base

	levelAfter := levelBefore
	if segmentPeriod == a.currentPeriod {
		levelAfter = levelForMiles(config.Thresholds, a.periodQualifying[segmentPeriod])
		if levelAfter < levelBefore {
			levelAfter = levelBefore
		}
		a.level = levelAfter
	}

	a.segments[in.Segment.ID] = &segmentRecord{
		qualifying: base,
		redeemable: gross,
		flownAt:    in.Segment.FlownAt,
		period:     segmentPeriod,
		posted:     true,
	}
	a.lastClockAt = in.Now
	a.lastActivityAt = in.Now
	a.frozen = false

	return CreditResult{
		Period:          segmentPeriod,
		LevelBefore:     levelBefore,
		LevelAfter:      levelAfter,
		BaseMiles:       base,
		QualifyingMiles: base,
		GrossRedeemable: gross,
		DebtRepaid:      debtRepaid,
		RedeemableAdded: added,
	}
}

func (a *account) refund(config Config, in RefundInput) RefundResult {
	record, exists := a.segments[in.SegmentID]
	if !exists {
		a.segments[in.SegmentID] = &segmentRecord{flownAt: -1}
		a.lastClockAt = in.Now
		return RefundResult{}
	}
	result := RefundResult{Seen: true, Period: record.period}
	if !record.posted {
		a.lastClockAt = in.Now
		return result
	}

	record.posted = false
	result.Posted = true
	result.QualifyingMiles = record.qualifying
	result.RedeemableMiles = record.redeemable
	a.periodQualifying[record.period] -= record.qualifying
	result.DebtCreated = a.deductRedeemable(record.redeemable)
	a.lastClockAt = in.Now
	return result
}

func (a *account) redeem(config Config, in RedeemInput) (RedeemResult, error) {
	before := a.redeemable
	a.redeemable -= in.CostMiles
	a.redemptions[in.RedemptionID] = &redemptionRecord{cost: in.CostMiles, at: in.Now}
	a.lastClockAt = in.Now
	a.lastActivityAt = in.Now
	a.frozen = false
	return RedeemResult{BalanceBefore: before, BalanceAfter: a.redeemable}, nil
}

func (a *account) cancelRedemption(config Config, in CancelRedemptionInput) (CancelRedemptionResult, error) {
	record, exists := a.redemptions[in.RedemptionID]
	if !exists {
		return CancelRedemptionResult{}, ErrRedemptionNotFound
	}
	if record.canceled {
		return CancelRedemptionResult{}, ErrRedemptionCanceled
	}
	record.canceled = true
	refund := record.cost - config.CancelFeeMiles
	if refund < 0 {
		refund = 0
	}
	debtRepaid, added := a.applyRedeemable(refund)
	a.lastClockAt = in.Now
	return CancelRedemptionResult{
		OriginallyDeducted: record.cost,
		Refunded:           refund,
		DebtRepaid:         debtRepaid,
		AddedToBalance:     added,
	}, nil
}

func (a *account) unfreeze(config Config, in UnfreezeInput) {
	a.rollForward(config, in.Now)
	a.frozen = false
	a.lastClockAt = in.Now
	a.lastActivityAt = in.Now
}

func (a *account) view() AccountView {
	return AccountView{
		ID:              a.id,
		Level:           a.level,
		QualifyingMiles: a.periodQualifying[a.currentPeriod],
		RedeemableMiles: a.redeemable,
		DebtMiles:       a.debt,
		Frozen:          a.frozen,
		CurrentPeriod:   a.currentPeriod,
		LastClockAt:     a.lastClockAt,
	}
}

func (a *account) viewAt(config Config, now int64) AccountView {
	view := a.view()
	wantPeriod := (now - a.openedAt) / config.PeriodSeconds
	if wantPeriod <= a.currentPeriod {
		view.Frozen = a.frozenAt(config, now)
		return view
	}

	level := a.level
	currentPeriod := a.currentPeriod
	for currentPeriod < wantPeriod {
		periodLevel := levelForMiles(config.Thresholds, a.periodQualifying[currentPeriod])
		floor := level - 1
		if floor < 0 {
			floor = 0
		}
		if periodLevel < floor {
			level = floor
		} else {
			level = periodLevel
		}
		currentPeriod++
	}
	view.Level = level
	view.CurrentPeriod = currentPeriod
	view.QualifyingMiles = a.periodQualifying[wantPeriod]
	view.Frozen = a.frozenAt(config, now)
	return view
}
