package ontology

// Naive reference model: every decision recomputes the signature chain from
// zero and linearly scans the revocation list. Records past their bound are
// treated as absent during scans and physically deleted on every accepted op.

type naiveRecord struct {
	id    string
	k     int
	sig   []byte
	bound int64
}

type naiveModel struct {
	cfg      Config
	clock    int64
	clockSet bool
	records  []naiveRecord
	macCalls int64
	queries  int64
	pops     int64
}

type naiveOutcome struct {
	ok         bool
	errKind    RejectKind
	prefix     int
	cavIndex   int
	cavProblem CaveatProblem
	expired    bool
	revoked    bool
	macCalls   int64
	queries    int64
	pops       int64
	active     int
	clock      int64
}

func (m *naiveModel) chain(id []byte, caveats [][]byte) [][]byte {
	sigs := make([][]byte, len(caveats)+1)
	sigs[0] = m.cfg.Mac(m.cfg.K, id)
	for j, c := range caveats {
		sigs[j+1] = m.cfg.Mac(sigs[j], c)
	}
	m.macCalls += int64(len(caveats) + 1)
	return sigs
}

// gc physically removes every record with bound <= now and counts pops.
func (m *naiveModel) gc(now int64) {
	kept := m.records[:0]
	for _, r := range m.records {
		if r.bound <= now {
			m.pops++
		} else {
			kept = append(kept, r)
		}
	}
	m.records = kept
}

func (m *naiveModel) accept(now int64) {
	m.clock, m.clockSet = now, true
	m.gc(now)
}

func (m *naiveModel) outcome(kind RejectKind) naiveOutcome {
	return naiveOutcome{
		errKind:  kind,
		macCalls: m.macCalls,
		queries:  m.queries,
		pops:     m.pops,
		active:   len(m.records),
		clock:    m.clock,
	}
}

func (m *naiveModel) mint(id []byte, caveats [][]byte, now int64) (*Token, naiveOutcome) {
	if len(id) < 1 || len(id) > 64 || !validMintCaveats(caveats, m.cfg) || !validNow(now) {
		o := m.outcome(RejectInvalid)
		o.ok = false
		return nil, o
	}
	if m.clockSet && now < m.clock {
		o := m.outcome(RejectClockRollback)
		return nil, o
	}
	m.accept(now)
	sigs := m.chain(id, caveats)
	o := m.outcome(0)
	o.ok = true
	return &Token{ID: id, Caveats: caveats, Sig: sigs[len(sigs)-1]}, o
}

func (m *naiveModel) verify(tok *Token, req Request, now int64) naiveOutcome {
	if !validTokenShape(tok, m.cfg) || !validRequest(req) || !validNow(now) {
		o := m.outcome(RejectInvalid)
		return o
	}
	if m.clockSet && now < m.clock {
		return m.outcome(RejectClockRollback)
	}
	m.accept(now)
	sigs := m.chain(tok.ID, tok.Caveats)
	if !bytesEqual(sigs[len(sigs)-1], tok.Sig) {
		return m.outcome(RejectBadSignature)
	}
	for j, sig := range sigs {
		m.queries++
		for _, r := range m.records {
			if r.bound > now && r.id == string(tok.ID) && r.k == j && bytesEqual(r.sig, sig) {
				o := m.outcome(RejectRevoked)
				o.prefix = j
				return o
			}
		}
	}
	for i, c := range tok.Caveats {
		p := parseCaveat(c)
		o := m.outcome(RejectCaveat)
		o.cavIndex = i + 1
		switch {
		case p.kind == cavUnknown:
			o.cavProblem = CaveatUnknown
			return o
		case !p.valid:
			o.cavProblem = CaveatMalformed
			return o
		case !p.satisfied(req, now):
			o.cavProblem = CaveatUnsatisfied
			return o
		}
	}
	o := m.outcome(0)
	o.ok = true
	return o
}

func (m *naiveModel) revoke(tok *Token, k int, now int64) (bool, bool, naiveOutcome) {
	if !validTokenShape(tok, m.cfg) || !validNow(now) || k < 0 || k > len(tok.Caveats) {
		return false, false, m.outcome(RejectInvalid)
	}
	if m.clockSet && now < m.clock {
		return false, false, m.outcome(RejectClockRollback)
	}
	sigs := m.chain(tok.ID, tok.Caveats)
	if !bytesEqual(sigs[len(sigs)-1], tok.Sig) {
		return false, false, m.outcome(RejectBadSignature)
	}
	keyID := string(tok.ID)
	for _, r := range m.records {
		if r.id == keyID && r.k == k {
			if r.bound <= now {
				m.accept(now)
				o := m.outcome(0)
				o.ok = true
				return false, true, o
			}
			m.accept(now)
			o := m.outcome(0)
			o.ok = true
			return true, false, o
		}
	}
	bound := prefixBound(tok.Caveats, k)
	if bound <= now {
		m.accept(now)
		o := m.outcome(0)
		o.ok = true
		return false, true, o
	}
	live := 0
	for _, r := range m.records {
		if r.bound > now {
			live++
		}
	}
	if live >= m.cfg.Rm {
		return false, false, m.outcome(RejectLimit)
	}
	m.accept(now)
	m.records = append(m.records, naiveRecord{
		id: keyID, k: k, sig: append([]byte(nil), sigs[k]...), bound: bound,
	})
	o := m.outcome(0)
	o.ok = true
	return true, false, o
}
