package greenreg

import "sort"

// usagePeriod is one user's registered usage for one consumption period.
type usagePeriod struct {
	qty    int64 // registered usage quantity
	units  int64 // sum of unitQty over currently valid retired certificates
	active int   // count of currently valid (non-revoked) retired certificates
}

// retirementBook tracks usage declarations, retirements and void events.
type retirementBook struct {
	unit  int64
	usage map[string]map[int64]*usagePeriod

	// Retired (possibly later revoked) certificates of one generation period,
	// ordered by retirement moment ascending; revocation iterates it in
	// reverse (latest retirement first).
	retiredByPeriod map[string]map[int64][]int64

	retireClock int64
}

func newRetirementBook(unit int64) *retirementBook {
	return &retirementBook{
		unit:            unit,
		usage:           make(map[string]map[int64]*usagePeriod),
		retiredByPeriod: make(map[string]map[int64][]int64),
	}
}

func (b *retirementBook) hasUsage(user string, period int64) bool {
	um := b.usage[user]
	return um != nil && um[period] != nil
}

func (b *retirementBook) registerUsage(user string, period, qty int64) *RegistryError {
	u := b.usage[user][period]
	if u == nil {
		u = &usagePeriod{}
		if b.usage[user] == nil {
			b.usage[user] = make(map[int64]*usagePeriod)
		}
		b.usage[user][period] = u
	}
	if qty < u.units {
		return newErr(ErrBelowRetired, "用电量修正低于已注销量")
	}
	u.qty = qty
	return nil
}

// applyRetire commits a batch retirement, assigning monotonically increasing
// retirement moments in the certificates' serial order.
func (b *retirementBook) applyRetire(user string, usagePeriod int64, certs []*Certificate) {
	u := b.usage[user][usagePeriod]
	for _, c := range certs {
		b.retireClock++
		c.Status = StatusRetired
		c.RetireUser = user
		c.UsagePeriod = usagePeriod
		c.RetireSeq = b.retireClock
		u.units += b.unit
		u.active++
		pm := b.retiredByPeriod[c.Facility]
		if pm == nil {
			pm = make(map[int64][]int64)
			b.retiredByPeriod[c.Facility] = pm
		}
		pm[c.Generation] = append(pm[c.Generation], c.Serial)
	}
}

// retiredForRevocation returns retired certificates of a generation period
// ordered by retirement moment descending (latest first).
func (b *retirementBook) retiredForRevocation(pool *certPool, facility string, period int64) []*Certificate {
	serials := b.retiredByPeriod[facility][period]
	out := make([]*Certificate, 0, len(serials))
	for _, s := range serials {
		if c := pool.get(s); c != nil && c.Status == StatusRetired {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RetireSeq > out[j].RetireSeq })
	return out
}

// voidRevoked removes the effect of revoking one retired certificate.
func (b *retirementBook) voidRevoked(c *Certificate) {
	u := b.usage[c.RetireUser][c.UsagePeriod]
	u.units -= b.unit
	u.active--
	c.Status = StatusRevoked
}
