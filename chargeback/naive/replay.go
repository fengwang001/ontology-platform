package naive

// rebuild re-applies every accepted op with time <= cutoff. Before op i is
// applied, automatic determinations are materialized up to the previous op's
// time (exactly what op i observed when it was accepted). After the loop,
// auto determinations advance to autoUntil.
func (m *Model) rebuild(cutoff, autoUntil int) *world {
	w := newWorld()
	n := 0
	for _, o := range m.ops {
		if o.now > cutoff {
			break
		}
		if n > 0 {
			m.applyAuto(w, m.ops[n-1].now)
		}
		m.applyOp(w, o)
		n++
	}
	m.applyAuto(w, autoUntil)
	return w
}

// replayForDecision reflects the world a candidate operation at now sees:
// only ops up to now, and auto determinations advanced to now.
func (m *Model) replayForDecision(now int) *world {
	return m.rebuild(now, now)
}

// replayForQuery reflects the world at read time now.
func (m *Model) replayForQuery(now int) *world {
	return m.rebuild(now, now)
}

func (m *Model) applyOp(w *world, o op) {
	switch o.kind {
	case opRegister:
		w.txn[o.txnID] = &Transaction{
			ID: o.txnID, CardID: o.card, MerchantID: o.merch,
			Amount: o.amount, SettlementDay: o.settle,
		}
	case opCredit:
		w.merchBal[o.merch] += o.amount
	case opFile:
		t := w.txn[o.txnID]
		w.cases[o.caseID] = &caseState{
			txnID: o.txnID, merch: t.MerchantID, reason: o.reason,
			amount: o.amount, fileDay: o.now, basisTxn: o.basis,
			status: StatusAwaitingDefense,
		}
		w.merchBal[t.MerchantID] -= o.amount
		w.pending += o.amount
	case opDefend:
		c := w.cases[o.caseID]
		c.status = StatusAwaitingReview
		c.defDay = o.now
	case opAccept:
		c := w.cases[o.caseID]
		c.status, c.outcome, c.termDay = StatusClosed, OutcomeMerchantWin, o.now
		w.pending -= c.amount
		w.merchBal[c.merch] += c.amount
	case opPreArb:
		w.cases[o.caseID].status = StatusPreArbitration
	case opRule:
		c := w.cases[o.caseID]
		c.status, c.termDay = StatusClosed, o.now
		if o.winner == OutcomeMerchantWin {
			c.outcome = OutcomeMerchantWin
			w.pending -= c.amount
			w.merchBal[c.merch] += c.amount
			w.issuer -= m.cfg.ArbitrationFee
		} else {
			c.outcome = OutcomeIssuerWin
			w.pending -= c.amount
			w.issuer += c.amount
			w.merchBal[c.merch] -= m.cfg.ArbitrationFee
		}
	}
}

// applyAuto closes cases whose first disallowed day is <= day, iterating to a
// fixed point so closures happen in deadline order regardless of map order.
func (m *Model) applyAuto(w *world, day int) {
	changed := true
	for changed {
		changed = false
		for _, c := range w.cases {
			switch c.status {
			case StatusAwaitingDefense:
				if day >= c.fileDay+m.cfg.DefenseDays+1 {
					c.status, c.outcome, c.termDay =
						StatusClosed, OutcomeIssuerWin, c.fileDay+m.cfg.DefenseDays+1
					w.pending -= c.amount
					w.issuer += c.amount
					changed = true
				}
			case StatusAwaitingReview:
				if day >= c.defDay+m.cfg.ReviewDays+1 {
					c.status, c.outcome, c.termDay =
						StatusClosed, OutcomeMerchantWin, c.defDay+m.cfg.ReviewDays+1
					w.pending -= c.amount
					w.merchBal[c.merch] += c.amount
					changed = true
				}
			}
		}
	}
}
