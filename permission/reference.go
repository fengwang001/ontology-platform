package permission

import (
	"sort"
	"time"
)

type NaiveReference struct {
	horizon    time.Time
	hasHorizon bool
	records    []Change
	withdrawn  map[string]bool
	audit      []AuditRecord
}

func NewNaiveReference() *NaiveReference {
	return &NaiveReference{withdrawn: make(map[string]bool)}
}

func (r *NaiveReference) Submit(change Change) (SubmitResult, error) {
	if err := validateChange(change); err != nil {
		r.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
		return SubmitResult{}, err
	}
	byID := make(map[string]Change)
	for _, existing := range r.records {
		if existing.ID == change.ID {
			err := &RuleError{Reason: "duplicate change id"}
			r.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
			return SubmitResult{}, err
		}
		byID[existing.ID] = existing
	}
	byID[change.ID] = change
	if change.Kind == Revoke {
		target, ok := byID[change.RevokesID]
		if !ok || target.Subject != change.Subject || target.Label != change.Label {
			err := &RuleError{Reason: "revocation target is not in the same subject-label timeline"}
			r.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
			return SubmitResult{}, err
		}
		if r.withdrawn[target.ID] {
			err := &RuleError{Reason: "revocation target has been withdrawn"}
			r.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
			return SubmitResult{}, err
		}
		if change.SubmittedAt.Before(target.SubmittedAt) {
			err := &RuleError{Reason: "revocation was submitted before its target"}
			r.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
			return SubmitResult{}, err
		}
		if referenceHasCycle(candidateRef(change), byID) {
			err := &RuleError{Reason: "revocation dependency cycle"}
			r.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
			return SubmitResult{}, err
		}
	}

	r.records = append(r.records, change)
	if !r.hasHorizon || change.SubmittedAt.Before(r.horizon) {
		r.horizon = change.SubmittedAt
		r.hasHorizon = true
	}
	result := SubmitResult{
		Accepted: true,
		OrderKey: change.EffectiveAt.Format(time.RFC3339Nano) + "|" +
			change.SubmittedAt.Format(time.RFC3339Nano) + "|" + change.ID,
	}
	r.appendAudit("Submit", formatChange(change), formatSubmitResult(result),
		"submission axis "+formatTime(change.SubmittedAt)+"; effective axis "+formatTime(change.EffectiveAt))
	return result, nil
}

func candidateRef(change Change) Change { return change }

func (r *NaiveReference) Withdraw(request WithdrawRequest) error {
	var record Change
	found := false
	for _, change := range r.records {
		if change.ID == request.ID {
			record, found = change, true
			break
		}
	}
	if !found {
		err := &RuleError{Reason: "unknown change id"}
		r.appendAudit("Withdraw", formatWithdraw(request), "REJECTED: "+err.Error(), err.Error())
		return err
	}
	if !request.WithdrawnAt.Before(record.EffectiveAt) {
		err := &RuleError{Code: ErrWithdrawAlreadyActive, Reason: "change has already reached its effective time"}
		r.appendAudit("Withdraw", formatWithdraw(request), "REJECTED: "+err.Error(), err.Error())
		return err
	}
	if request.WithdrawnAt.Before(record.SubmittedAt) {
		err := &RuleError{Reason: "withdrawal time is before the change submission time"}
		r.appendAudit("Withdraw", formatWithdraw(request), "REJECTED: "+err.Error(), err.Error())
		return err
	}
	if r.withdrawn[record.ID] {
		err := &RuleError{Reason: "change is already withdrawn"}
		r.appendAudit("Withdraw", formatWithdraw(request), "REJECTED: "+err.Error(), err.Error())
		return err
	}

	r.withdrawn[record.ID] = true
	r.appendAudit("Withdraw", formatWithdraw(request), "ACCEPTED",
		"withdrawal precedes effective boundary "+formatTime(record.EffectiveAt))
	return nil
}

func (r *NaiveReference) Decide(query Query) (QueryResult, error) {
	if r.hasHorizon && query.At.Before(r.horizon) {
		err := &RuleError{Code: ErrQueryBeforeHorizon, Reason: "query time precedes the earliest known record"}
		r.appendAudit("Decide", formatQuery(query), "REJECTED: "+err.Error(), err.Error())
		return QueryResult{}, err
	}

	changes := make(map[string]Change)
	var ordered []Change
	for _, change := range r.records {
		if change.Subject == query.Subject && change.Label == query.Label &&
			!change.SubmittedAt.After(query.At) && !change.EffectiveAt.After(query.At) &&
			!r.withdrawn[change.ID] {
			changes[change.ID] = change
			ordered = append(ordered, change)
		}
	}
	sortRefChanges(ordered)

	active := make(map[string]bool)
	revokerCount := make(map[string]int)
	firstActive := make(map[string]time.Time)
	everActive := make(map[string]bool)
	eventOrder := make(map[string]int)
	dependents := make(map[string][]Change)
	for _, change := range ordered {
		if change.Kind == Revoke {
			dependents[change.RevokesID] = append(dependents[change.RevokesID], change)
		}
	}

	canActivate := func(change Change) bool {
		if active[change.ID] || revokerCount[change.ID] > 0 {
			return false
		}
		if change.Kind == Revoke {
			target := changes[change.RevokesID]
			return target.Subject != "" && active[target.ID]
		}
		return true
	}
	var deactivate func(change Change, at time.Time, pending *[]Change)
	var activate func(change Change, at time.Time, pending *[]Change)
	activate = func(change Change, at time.Time, pending *[]Change) {
		if active[change.ID] {
			return
		}
		active[change.ID] = true
		everActive[change.ID] = true
		if _, exists := eventOrder[change.ID]; !exists {
			eventOrder[change.ID] = len(eventOrder)
		}
		firstActive[change.ID] = at
		if change.Kind == Revoke {
			revokerCount[change.RevokesID]++
		}
		*pending = append(*pending, dependents[change.ID]...)
		if change.Kind == Revoke {
			target := changes[change.RevokesID]
			if target.Subject != "" && active[target.ID] {
				deactivate(target, at, pending)
			}
		}
	}
	deactivate = func(change Change, at time.Time, pending *[]Change) {
		if !active[change.ID] {
			return
		}
		active[change.ID] = false
		*pending = append(*pending, dependents[change.ID]...)
		if change.Kind == Revoke {
			revokerCount[change.RevokesID]--
			target := changes[change.RevokesID]
			if target.Subject != "" && !target.EffectiveAt.After(at) {
				*pending = append(*pending, target)
			}
		}
	}

	for groupStart := 0; groupStart < len(ordered); {
		at := ordered[groupStart].EffectiveAt
		groupEnd := groupStart + 1
		for groupEnd < len(ordered) && ordered[groupEnd].EffectiveAt.Equal(at) {
			groupEnd++
		}

		pending := append([]Change(nil), ordered[groupStart:groupEnd]...)
		for len(pending) > 0 {
			nextIndex := 0
			for index := 1; index < len(pending); index++ {
				left, right := pending[index], pending[nextIndex]
				if !left.SubmittedAt.Equal(right.SubmittedAt) {
					if left.SubmittedAt.Before(right.SubmittedAt) {
						nextIndex = index
					}
				} else if left.ID < right.ID {
					nextIndex = index
				}
			}
			change := pending[nextIndex]
			pending = append(pending[:nextIndex], pending[nextIndex+1:]...)
			if change.EffectiveAt.After(at) || !canActivate(change) {
				continue
			}

			activate(change, at, &pending)
		}
		groupStart = groupEnd
	}

	var examined []string
	for id := range everActive {
		if at := firstActive[id]; !at.After(query.At) {
			examined = append(examined, id)
		}
	}
	sort.Slice(examined, func(i, j int) bool {
		left := firstActive[examined[i]]
		right := firstActive[examined[j]]
		if !left.Equal(right) {
			return left.Before(right)
		}
		leftChange := changes[examined[i]]
		rightChange := changes[examined[j]]
		if !leftChange.SubmittedAt.Equal(rightChange.SubmittedAt) {
			return leftChange.SubmittedAt.Before(rightChange.SubmittedAt)
		}
		return examined[i] < examined[j]
	})

	var winner *Change
	for index := range ordered {
		candidate := &ordered[index]
		if active[candidate.ID] && candidate.Kind == Put &&
			(winner == nil || refOrder(*candidate, *winner) > 0) {
			winner = candidate
		}
	}

	result := QueryResult{Decision: Deny, ExaminedChangeID: examined}
	basis := "no active put; default deny"
	if winner != nil {
		result.Decision = winner.Decision
		result.WinningChangeID = winner.ID
		basis = "latest active put in (effective_at, submitted_at, id) order is " + winner.ID
	}
	r.appendAudit("Decide", formatQuery(query), formatQueryResult(result), basis)
	return result, nil
}

func (r *NaiveReference) AuditLog() []AuditRecord {
	records := make([]AuditRecord, len(r.audit))
	copy(records, r.audit)
	return records
}

func (r *NaiveReference) appendAudit(call, input, output, basis string) {
	r.audit = append(r.audit, AuditRecord{Call: call, Input: input, Output: output, TemporalBasis: basis})
}

func sortRefChanges(changes []Change) {
	sort.Slice(changes, func(i, j int) bool { return refOrder(changes[i], changes[j]) < 0 })
}

func refOrder(left, right Change) int {
	if !left.EffectiveAt.Equal(right.EffectiveAt) {
		if left.EffectiveAt.Before(right.EffectiveAt) {
			return -1
		}
		return 1
	}
	if !left.SubmittedAt.Equal(right.SubmittedAt) {
		if left.SubmittedAt.Before(right.SubmittedAt) {
			return -1
		}
		return 1
	}
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return 0
}

func referenceHasCycle(candidate Change, byID map[string]Change) bool {
	seen := map[string]bool{candidate.ID: true}
	current := candidate
	for current.Kind == Revoke {
		next := byID[current.RevokesID]
		if next.Subject == "" {
			return false
		}
		if seen[next.ID] {
			return true
		}
		seen[next.ID] = true
		current = next
	}
	return false
}
