package greenreg

import "sort"

// certPool stores every certificate ever issued. Certificate serials are the
// dense 1..nextSerial-1 range, so existence is an O(1) bounds check and
// rejected issuance never consumes a serial.
type certPool struct {
	certs      []*Certificate // index = serial-1; append-only
	nextSerial int64

	// Per facility+generation-period ordering, used only by revocation
	// selection. Cost is proportional to that period's certificate count.
	byPeriod map[string]map[int64][]int64
}

func newCertPool() *certPool {
	return &certPool{nextSerial: 1, byPeriod: make(map[string]map[int64][]int64)}
}

func (p *certPool) exists(serial int64) bool {
	return serial >= 1 && serial <= int64(len(p.certs))
}

func (p *certPool) get(serial int64) *Certificate {
	if !p.exists(serial) {
		return nil
	}
	return p.certs[serial-1]
}

func (p *certPool) issue(facility string, period int64, holder string) *Certificate {
	c := &Certificate{
		Serial:     p.nextSerial,
		Facility:   facility,
		Generation: period,
		Holder:     holder,
		Status:     StatusHeld,
	}
	p.certs = append(p.certs, c)
	p.nextSerial++
	pm := p.byPeriod[facility]
	if pm == nil {
		pm = make(map[int64][]int64)
		p.byPeriod[facility] = pm
	}
	pm[period] = append(pm[period], c.Serial)
	return c
}

func (p *certPool) count() int { return len(p.certs) }

// heldForPeriod returns the held certificates of one generation period sorted
// by serial descending (the revocation order for live certificates).
func (p *certPool) heldForPeriod(facility string, period int64) []*Certificate {
	var out []*Certificate
	for _, s := range p.byPeriod[facility][period] {
		c := p.certs[s-1]
		if c.Status == StatusHeld {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Serial > out[j].Serial })
	return out
}

// liveForPeriod returns non-revoked certificates (held or retired) of a period
// sorted ascending by serial, for invariant checks.
func (p *certPool) liveForPeriod(facility string, period int64) []*Certificate {
	var out []*Certificate
	for _, s := range p.byPeriod[facility][period] {
		c := p.certs[s-1]
		if c.Status != StatusRevoked {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Serial < out[j].Serial })
	return out
}
