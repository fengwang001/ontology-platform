package naive

func (m *Model) queryWorld(now int) (*world, error) {
	if now < m.last {
		return nil, ErrClockRollback
	}
	return m.replayForQuery(now), nil
}

func (m *Model) MerchantBalance(now int, merchantID string) (int64, error) {
	if merchantID == "" {
		return 0, ErrInvalidArgument
	}
	w, err := m.queryWorld(now)
	if err != nil {
		return 0, err
	}
	return w.merchBal[merchantID], nil
}

func (m *Model) IssuerAccount(now int) (int64, error) {
	w, err := m.queryWorld(now)
	if err != nil {
		return 0, err
	}
	return w.issuer, nil
}

func (m *Model) PendingHeld(now int) (int64, error) {
	w, err := m.queryWorld(now)
	if err != nil {
		return 0, err
	}
	return w.pending, nil
}

func (m *Model) Disputable(now int, txnID string) (int64, error) {
	if txnID == "" {
		return 0, ErrInvalidArgument
	}
	if now < m.last {
		return 0, ErrClockRollback
	}
	w := m.replayForQuery(now)
	t := w.txn[txnID]
	if t == nil {
		return 0, ErrTransactionNotFound
	}
	return t.Amount - w.liveOccupancy(txnID), nil
}

func (m *Model) CaseStatus(now int, caseID string) (Status, Outcome, error) {
	if caseID == "" {
		return 0, 0, ErrInvalidArgument
	}
	w, err := m.queryWorld(now)
	if err != nil {
		return 0, 0, err
	}
	c := w.cases[caseID]
	if c == nil {
		return 0, 0, ErrCaseNotFound
	}
	return c.status, c.outcome, nil
}
