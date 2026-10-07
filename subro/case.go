package subro

// caseState is the mutable per-case state. It keeps only fixed-size
// aggregates plus an append-only recovery log, so settling after an
// accepted operation never iterates over history.
type caseState struct {
	id           string
	totalLoss    int64
	insurerPaid  int64
	deadline     int64
	ratioBP      int64
	waived       bool
	grossTotal   int64
	expenseTotal int64
	netTotal     int64
	paid         Entitlement
	recoveries   []Recovery
}

func (c *caseState) uncompensated() int64 {
	return c.totalLoss - c.insurerPaid
}

func (c *caseState) cap() int64 {
	return recoverableCap(c.totalLoss, c.ratioBP)
}

func (c *caseState) entitled() Entitlement {
	return allocate(c.netTotal, c.cap(), c.uncompensated(), c.insurerPaid, c.waived)
}

func (c *caseState) snapshot() CaseSnapshot {
	return CaseSnapshot{
		ID:            c.id,
		TotalLoss:     c.totalLoss,
		InsurerPaid:   c.insurerPaid,
		Uncompensated: c.uncompensated(),
		RatioBP:       c.ratioBP,
		Deadline:      c.deadline,
		Waived:        c.waived,
		GrossTotal:    c.grossTotal,
		ExpenseTotal:  c.expenseTotal,
		NetTotal:      c.netTotal,
		Cap:           c.cap(),
		Entitled:      c.entitled(),
		Paid:          c.paid,
		Recoveries:    len(c.recoveries),
	}
}
