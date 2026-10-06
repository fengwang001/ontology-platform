package aml

import "sort"

func (s *System) newGroupLocked() *group {
	s.nextID++
	return &group{
		id:       s.nextID,
		accounts: make(map[string]*account),
		buckets:  make(map[int64]map[string]*deposit),
	}
}

func (s *System) mergeGroupsLocked(dst, src *group, now int64) {
	dst.hasWindow = true
	dst.lastNow = now
	mergedBuckets := make(map[int64]map[string]*deposit)
	for id, acct := range src.accounts {
		acct.groupID = dst.id
		dst.accounts[id] = acct
	}
	for day := 0; day < int(s.cfg.D); day++ {
		date := subtractDays(now, day)
		mergedBucket := make(map[string]*deposit)
		for _, sourceBucket := range []map[string]*deposit{dst.buckets[date], src.buckets[date]} {
			for txID, dep := range sourceBucket {
				mergedBucket[txID] = dep
			}
		}
		if len(mergedBucket) > 0 {
			mergedBuckets[date] = mergedBucket
		}
	}
	dst.buckets = mergedBuckets
	delete(s.groups, src.id)
}

func (s *System) addDepositToBucketLocked(g *group, dep *deposit) {
	if !g.hasWindow {
		g.lastNow = dep.date
		g.hasWindow = true
	} else if gap := elapsedDays(g.lastNow, dep.date); gap >= s.cfg.D {
		g.buckets = make(map[int64]map[string]*deposit)
	} else {
		for _, date := range expireDates(g.lastNow, dep.date, s.cfg.D) {
			delete(g.buckets, date)
		}
	}
	g.lastNow = dep.date

	bucket := g.buckets[dep.date]
	if bucket == nil {
		bucket = make(map[string]*deposit)
		g.buckets[dep.date] = bucket
	}
	bucket[dep.txID] = dep
}

func (s *System) windowDepositsLocked(g *group, now int64) []*deposit {
	var deposits []*deposit
	for day := 0; day < int(s.cfg.D); day++ {
		date := subtractDays(now, day)
		for _, dep := range g.buckets[date] {
			if !dep.reversed {
				deposits = append(deposits, dep)
			}
		}
	}
	sort.Slice(deposits, func(i, j int) bool {
		if deposits[i].date != deposits[j].date {
			return deposits[i].date < deposits[j].date
		}
		return deposits[i].txID < deposits[j].txID
	})
	return deposits
}

func (s *System) evaluateStructuredLocked(g *group, now int64, trigger OperationRef) *Report {
	deposits := s.windowDepositsLocked(g, now)
	summary := summarizeDeposits(deposits)
	if summary.Count < s.cfg.K || summary.Total < s.cfg.H {
		return nil
	}

	hasUncovered := false
	for _, dep := range deposits {
		if !dep.covered {
			hasUncovered = true
			break
		}
	}
	if !hasUncovered {
		return nil
	}

	txIDs := make([]string, 0, len(deposits))
	for _, dep := range deposits {
		dep.covered = true
		txIDs = append(txIDs, dep.txID)
	}

	report := Report{
		Number:         int64(len(s.reports)) + 1,
		Kind:           StructuredReport,
		Trigger:        trigger,
		At:             now,
		GroupID:        g.id,
		Accounts:       s.sortedAccountsLocked(g),
		TransactionIDs: txIDs,
		Total:          summary.Total,
	}
	s.reports = append(s.reports, report)
	return &s.reports[len(s.reports)-1]
}

func (s *System) newLargeReportLocked(groupID, now int64, dep *deposit) Report {
	g := s.groups[groupID]
	report := Report{
		Number:         int64(len(s.reports)) + 1,
		Kind:           LargeReport,
		Trigger:        OperationRef{Kind: DepositOperation, ID: dep.txID},
		At:             now,
		GroupID:        groupID,
		Accounts:       s.sortedAccountsLocked(g),
		TransactionIDs: []string{dep.txID},
		Total:          dep.amount,
	}
	s.reports = append(s.reports, report)
	return s.reports[len(s.reports)-1]
}

func (s *System) sortedAccountsLocked(g *group) []string {
	accounts := make([]string, 0, len(g.accounts))
	for id := range g.accounts {
		accounts = append(accounts, id)
	}
	sort.Strings(accounts)
	return accounts
}

func (s *System) windowSummaryLocked(g *group, now int64) WindowSummary {
	return summarizeDeposits(s.windowDepositsLocked(g, now))
}

func summarizeDeposits(deposits []*deposit) WindowSummary {
	summary := WindowSummary{Count: int64(len(deposits))}
	for _, dep := range deposits {
		summary.Total = saturatingAdd(summary.Total, dep.amount)
	}
	return summary
}
