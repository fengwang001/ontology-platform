package naive

type caseState struct {
	txnID    string
	merch    string
	reason   Reason
	amount   int64
	fileDay  int
	defDay   int
	status   Status
	outcome  Outcome
	termDay  int
	basisTxn string
}

type world struct {
	txn      map[string]*Transaction
	merchBal map[string]int64
	issuer   int64
	pending  int64
	cases    map[string]*caseState
}

func newWorld() *world {
	return &world{
		txn:      map[string]*Transaction{},
		merchBal: map[string]int64{},
		cases:    map[string]*caseState{},
	}
}

// liveOccupancy is the amount on a transaction that is in progress or ended
// issuer-win. It is computed by a direct scan (the naive O(n) approach).
func (w *world) liveOccupancy(txnID string) int64 {
	var sum int64
	for _, c := range w.cases {
		if c.txnID != txnID {
			continue
		}
		if c.status != StatusClosed || c.outcome == OutcomeIssuerWin {
			sum += c.amount
		}
	}
	return sum
}

// basisLocked reports whether basisTxn currently supports a live duplicate
// case (anything not merchant-win), excluding exceptCase.
func (w *world) basisLocked(basisTxn, exceptCase string) bool {
	for id, c := range w.cases {
		if id == exceptCase {
			continue
		}
		if c.reason != ReasonDuplicate || c.basisTxn != basisTxn {
			continue
		}
		if c.status != StatusClosed || c.outcome != OutcomeMerchantWin {
			return true
		}
	}
	return false
}

func (w *world) findBasis(t *Transaction, rangeDays int) *Transaction {
	var best *Transaction
	for _, b := range w.txn {
		if b.ID == t.ID || b.CardID != t.CardID ||
			b.MerchantID != t.MerchantID || b.Amount != t.Amount {
			continue
		}
		diff := t.SettlementDay - b.SettlementDay
		if diff > 0 && diff <= rangeDays && (best == nil || b.SettlementDay > best.SettlementDay) {
			best = b
		}
	}
	return best
}

func (w *world) merchantWinOnReason(txnID string, r Reason) bool {
	for _, c := range w.cases {
		if c.txnID == txnID && c.reason == r &&
			c.status == StatusClosed && c.outcome == OutcomeMerchantWin {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
