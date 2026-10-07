package ncdengine

import "sort"

type account struct {
	config     Config
	terms      []PolicyTerm
	claims     map[string]Claim
	termClaims map[int][]Claim
	deltas     []PremiumDelta
}

func newAccount(config Config) *account {
	return &account{
		config:     config,
		claims:     make(map[string]Claim),
		termClaims: make(map[int][]Claim),
	}
}

func (a *account) latestTerm() (PolicyTerm, bool) {
	if len(a.terms) == 0 {
		return PolicyTerm{}, false
	}
	return a.terms[len(a.terms)-1], true
}

func (a *account) termAt(day int) (int, bool) {
	if len(a.terms) == 0 {
		return 0, false
	}
	index := len(a.terms) - 1
	index = sort.Search(len(a.terms), func(i int) bool {
		return day < a.terms[i].EndDay
	})
	if index >= len(a.terms) {
		return 0, false
	}
	term := a.terms[index]
	return index, day >= term.StartDay && day < term.EndDay
}

func (a *account) isRenewable(term PolicyTerm, day int) bool {
	return withinRenewalWindow(term.EndDay, day, a.config.RenewalGraceDays)
}

func (a *account) hasActivePolicy(day int) bool {
	term, ok := a.latestTerm()
	if !ok {
		return false
	}
	return day < term.EndDay+a.config.RenewalGraceDays
}

func (a *account) addClaim(claim Claim) (int, bool) {
	index, ok := a.termAt(claim.AccidentDay)
	if !ok {
		return 0, false
	}
	a.claims[claim.ID] = claim
	startDay := a.terms[index].StartDay
	a.termClaims[startDay] = append(a.termClaims[startDay], claim)
	return index, true
}

func (a *account) removeClaim(claim Claim) (int, bool) {
	index, ok := a.termAt(claim.AccidentDay)
	if !ok {
		return 0, false
	}
	delete(a.claims, claim.ID)
	startDay := a.terms[index].StartDay
	listed := a.termClaims[startDay]
	for i, existing := range listed {
		if existing.ID == claim.ID {
			a.termClaims[startDay] = append(listed[:i], listed[i+1:]...)
			break
		}
	}
	return index, true
}

func (a *account) recompute(fromIndex int, customerID string, claimID string, deletedClaim bool) {
	if fromIndex < 0 {
		return
	}

	if fromIndex >= len(a.terms)-1 {
		return
	}

	level := a.terms[fromIndex].RenewalLevel
	for index := fromIndex + 1; index < len(a.terms); index++ {
		term := a.terms[index]
		if term.StartDay != a.terms[index-1].EndDay {
			break
		}
		oldLevel := term.RenewalLevel
		initialLevel := a.terms[index-1].RenewalLevel
		term.InitialLevel = level
		level = nextLevel(initialLevel, a.termClaims[a.terms[index-1].StartDay], a.terms[index-1].Protection, a.config)
		term.RenewalLevel = level
		a.terms[index] = term
		if level < oldLevel {
			a.deltas = append(a.deltas, PremiumDelta{
				ClaimID:      claimID,
				CustomerID:   customerID,
				StartDay:     term.StartDay,
				OldLevel:     oldLevel,
				NewLevel:     level,
				Amount:       a.config.Premiums[level] - a.config.Premiums[oldLevel],
				DeletedClaim: deletedClaim,
			})
		}
	}
}
