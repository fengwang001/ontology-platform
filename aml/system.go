package aml

import "sync"

type System struct {
	cfg Config

	mu       sync.RWMutex
	now      int64
	clockSet bool
	nextID   int64
	accounts map[string]*account
	groups   map[int64]*group
	deposits map[string]*deposit
	reports  []Report
}

// NewSystem creates an AML reporting system with validated thresholds.
func NewSystem(cfg Config) (*System, error) {
	if cfg.L <= 0 || cfg.H <= 0 || cfg.L >= cfg.H || cfg.K < 2 || cfg.D < 1 {
		return nil, ErrInvalidArgument
	}

	return &System{
		cfg:      cfg,
		accounts: make(map[string]*account),
		groups:   make(map[int64]*group),
		deposits: make(map[string]*deposit),
	}, nil
}

// OpenAccount creates a new account that initially owns its own customer group.
func (s *System) OpenAccount(now int64, accountID string) error {
	if accountID == "" {
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.clockSet && now < s.now {
		return ErrClockRollback
	}
	if _, exists := s.accounts[accountID]; exists {
		return ErrAccountAlreadyExists
	}

	group := s.newGroupLocked()
	s.groups[group.id] = group
	acct := &account{id: accountID, groupID: group.id}
	group.accounts[accountID] = acct
	s.accounts[accountID] = acct
	s.clockSet = true
	s.now = now
	return nil
}

// Deposit records a uniquely numbered cash deposit and returns the one report it may create.
func (s *System) Deposit(now int64, accountID, txID string, amount int64) (*Report, error) {
	if accountID == "" || txID == "" || amount <= 0 {
		return nil, ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.clockSet && now < s.now {
		return nil, ErrClockRollback
	}
	acct, ok := s.accounts[accountID]
	if !ok {
		return nil, ErrAccountNotFound
	}
	if _, exists := s.deposits[txID]; exists {
		return nil, ErrDuplicateTransaction
	}

	dep := &deposit{
		txID:      txID,
		accountID: accountID,
		amount:    amount,
		date:      now,
	}
	s.deposits[txID] = dep
	s.clockSet = true
	s.now = now

	if amount >= s.cfg.H {
		report := s.newLargeReportLocked(acct.groupID, now, dep)
		return cloneReportPointer(&report), nil
	}

	if amount >= s.cfg.L {
		group := s.groups[acct.groupID]
		s.addDepositToBucketLocked(group, dep)
		if report := s.evaluateStructuredLocked(group, now, OperationRef{Kind: DepositOperation, ID: txID}); report != nil {
			return cloneReportPointer(report), nil
		}
	}
	return nil, nil
}

// Link irreversibly merges the customer groups containing the two accounts.
func (s *System) Link(now int64, accountIDA, accountIDB string) (*Report, error) {
	if accountIDA == "" || accountIDB == "" {
		return nil, ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.clockSet && now < s.now {
		return nil, ErrClockRollback
	}
	acctA, ok := s.accounts[accountIDA]
	if !ok {
		return nil, ErrAccountNotFound
	}
	acctB, ok := s.accounts[accountIDB]
	if !ok {
		return nil, ErrAccountNotFound
	}
	if acctA.groupID == acctB.groupID {
		return nil, ErrAlreadyLinked
	}

	groupA := s.groups[acctA.groupID]
	groupB := s.groups[acctB.groupID]
	dst, src := groupA, groupB
	if len(dst.accounts) < len(src.accounts) {
		dst, src = src, dst
	}
	s.mergeGroupsLocked(dst, src, now)
	s.clockSet = true
	s.now = now

	firstID, secondID := accountIDA, accountIDB
	if secondID < firstID {
		firstID, secondID = secondID, firstID
	}
	trigger := OperationRef{Kind: LinkOperation, ID: firstID + "->" + secondID}
	report := s.evaluateStructuredLocked(dst, now, trigger)
	if report != nil {
		return cloneReportPointer(report), nil
	}
	return nil, nil
}

// Reverse voids a deposit without withdrawing any report that was already issued.
func (s *System) Reverse(now int64, txID string) error {
	if txID == "" {
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.clockSet && now < s.now {
		return ErrClockRollback
	}
	dep, ok := s.deposits[txID]
	if !ok {
		return ErrTransactionNotFound
	}
	if dep.reversed {
		return ErrAlreadyReversed
	}

	acct := s.accounts[dep.accountID]
	group := s.groups[acct.groupID]
	if bucket := group.buckets[dep.date]; bucket != nil {
		delete(bucket, txID)
		if len(bucket) == 0 {
			delete(group.buckets, dep.date)
		}
	}
	dep.reversed = true
	s.clockSet = true
	s.now = now
	return nil
}

// GroupAccounts returns every account in the queried account's current customer group.
func (s *System) GroupAccounts(now int64, accountID string) ([]string, error) {
	if accountID == "" {
		return nil, ErrInvalidArgument
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.clockSet && now < s.now {
		return nil, ErrClockRollback
	}
	acct, ok := s.accounts[accountID]
	if !ok {
		return nil, ErrAccountNotFound
	}
	return s.sortedAccountsLocked(s.groups[acct.groupID]), nil
}

// WindowSummary returns the structured-deposit count and total for the current query time.
func (s *System) WindowSummary(now int64, accountID string) (WindowSummary, error) {
	if accountID == "" {
		return WindowSummary{}, ErrInvalidArgument
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.clockSet && now < s.now {
		return WindowSummary{}, ErrClockRollback
	}
	acct, ok := s.accounts[accountID]
	if !ok {
		return WindowSummary{}, ErrAccountNotFound
	}
	return s.windowSummaryLocked(s.groups[acct.groupID], now), nil
}

// Reports returns a snapshot of all reports in global issue order.
func (s *System) Reports(now int64) ([]Report, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.clockSet && now < s.now {
		return nil, ErrClockRollback
	}
	reports := make([]Report, len(s.reports))
	for i, report := range s.reports {
		reports[i] = cloneReport(report)
	}
	return reports, nil
}
