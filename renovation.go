package ontology

type PermitStatus int

const (
	PermitPending PermitStatus = iota
	PermitApproved
	PermitActive
	PermitFinished
	PermitSuspended
	PermitRevoked
)

type Permit struct {
	ID            string
	House         string
	StartDay      int
	EndDay        int
	Noisy         bool
	Status        PermitStatus
	Extended      bool
	Suspended     bool
	CheckedIn     bool
	Complaints    int
	LastComplaint int
	RevokedDay    int
}

type QuietRange struct {
	StartMinute int
	EndMinute   int
}

type renovationOffice struct {
	records       map[string]*Permit
	active        map[string]*Permit
	lastRevokeDay map[string]int
	quietRanges   []QuietRange
	holidays      map[int]bool
}

func newRenovationOffice() *renovationOffice {
	return &renovationOffice{
		records:       map[string]*Permit{},
		active:        map[string]*Permit{},
		lastRevokeDay: map[string]int{},
		holidays:      map[int]bool{},
	}
}

func (o *renovationOffice) apply(p Permit, waitDays int) ErrorCode {
	if o.active[p.House] != nil {
		return ErrIllegalState
	}
	if revokedDay, ok := o.lastRevokeDay[p.House]; ok && p.StartDay < revokedDay+waitDays {
		return ErrTimeWindow
	}
	pp := p
	pp.Status = PermitPending
	o.records[pp.ID] = &pp
	o.active[pp.House] = &pp
	return OK
}

func (o *renovationOffice) approve(id string) ErrorCode {
	p := o.records[id]
	if p == nil {
		return ErrNotFound
	}
	if o.active[p.House] != p || p.Status != PermitPending {
		return ErrIllegalState
	}
	p.Status = PermitApproved
	return OK
}

func (o *renovationOffice) checkIn(id string, now int) ErrorCode {
	p := o.records[id]
	if p == nil {
		return ErrNotFound
	}
	if o.active[p.House] != p {
		return ErrIllegalState
	}
	if p.Status == PermitRevoked {
		return ErrIllegalState
	}
	if p.Suspended || p.Status == PermitSuspended {
		return ErrIllegalState
	}
	if p.Status != PermitApproved && p.Status != PermitActive {
		return ErrIllegalState
	}
	day := floorDay(now)
	if day < p.StartDay || day >= p.EndDay {
		return ErrTimeWindow
	}
	if p.Noisy && o.isQuiet(now) {
		return ErrQuietConflict
	}
	p.Status = PermitActive
	p.CheckedIn = true
	return OK
}

func (o *renovationOffice) checkOut(id string) ErrorCode {
	p := o.records[id]
	if p == nil || o.active[p.House] != p || !p.CheckedIn {
		return ErrIllegalState
	}
	p.CheckedIn = false
	if p.Suspended {
		p.Status = PermitSuspended
	} else {
		p.Status = PermitApproved
	}
	return OK
}

func (o *renovationOffice) extend(id string, now, newEndDay, maxDays int) ErrorCode {
	p := o.records[id]
	if p == nil {
		return ErrNotFound
	}
	if o.active[p.House] != p {
		return ErrIllegalState
	}
	if p.Extended || floorDay(now) >= p.EndDay {
		return ErrTimeWindow
	}
	if newEndDay <= p.EndDay || newEndDay-p.StartDay > maxDays {
		return ErrTimeWindow
	}
	p.EndDay = newEndDay
	p.Extended = true
	return OK
}

func (o *renovationOffice) complain(house string, now, revokeAt int) ErrorCode {
	p := o.active[house]
	if p == nil {
		return ErrNotFound
	}
	day := floorDay(now)
	p.Complaints++
	p.LastComplaint = day
	if p.Complaints >= revokeAt {
		p.Status = PermitRevoked
		p.Suspended = false
		p.CheckedIn = false
		p.RevokedDay = day
		o.lastRevokeDay[house] = day
		delete(o.active, house)
	} else {
		p.Suspended = true
		p.Status = PermitSuspended
	}
	return OK
}

func (o *renovationOffice) liftSuspension(house string) ErrorCode {
	p := o.active[house]
	if p == nil {
		return ErrNotFound
	}
	if !p.Suspended {
		return ErrIllegalState
	}
	p.Suspended = false
	p.Status = PermitApproved
	return OK
}

func (o *renovationOffice) finishDue(now int) {
	day := floorDay(now)
	for house, p := range o.active {
		if day >= p.EndDay && !p.CheckedIn && p.Status != PermitRevoked {
			p.Status = PermitFinished
			delete(o.active, house)
		}
	}
}

func (o *renovationOffice) setQuietRanges(ranges []QuietRange) {
	o.quietRanges = append([]QuietRange(nil), ranges...)
}

func (o *renovationOffice) addHoliday(day int) { o.holidays[day] = true }

func (o *renovationOffice) isQuiet(minute int) bool {
	day := floorDay(minute)
	if o.holidays[day] {
		return true
	}
	for _, r := range o.quietRanges {
		if r.StartMinute < r.EndMinute {
			start := day*1440 + r.StartMinute
			end := day*1440 + r.EndMinute
			if minute >= start && minute < end {
				return true
			}
			continue
		}
		if r.StartMinute > r.EndMinute {
			start := day*1440 + r.StartMinute
			end := (day+1)*1440 + r.EndMinute
			if minute >= start && minute < end {
				return true
			}
			prevStart := (day-1)*1440 + r.StartMinute
			prevEnd := day*1440 + r.EndMinute
			if minute >= prevStart && minute < prevEnd {
				return true
			}
		}
	}
	return false
}

func (o *renovationOffice) refundEligible(house string, now, refundDays int) (*Permit, bool) {
	var latest *Permit
	for _, p := range o.records {
		if p.House != house || o.active[house] == p {
			continue
		}
		if latest == nil || p.EndDay > latest.EndDay || p.RevokedDay > latest.RevokedDay {
			latest = p
		}
	}
	if latest == nil {
		return nil, false
	}
	anchor := latest.EndDay
	if latest.RevokedDay+1 > anchor {
		anchor = latest.RevokedDay + 1
	}
	if latest.LastComplaint+1 > anchor {
		anchor = latest.LastComplaint + 1
	}
	return latest, floorDay(now) >= anchor+refundDays
}

func (o *renovationOffice) get(id string) (*Permit, bool) {
	p, ok := o.records[id]
	return p, ok
}
