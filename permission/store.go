package permission

import (
	"sort"
	"sync"
	"time"
)

type changeRecord struct {
	Change
	withdrawn bool
}

type stateEvent struct {
	at     time.Time
	change *changeRecord
	active bool
	order  int
}

type keyState struct {
	records      []*changeRecord
	byID         map[string]*changeRecord
	dependents   map[string][]*changeRecord
	revokerCount map[string]int
	events       []stateEvent
	cursor       int
	active       map[string]bool
}

type Store struct {
	mu sync.RWMutex

	horizon    time.Time
	hasHorizon bool
	records    map[string]*changeRecord
	keys       map[string]*keyState
	audit      []AuditRecord
}

func NewStore() *Store {
	return &Store{
		records: make(map[string]*changeRecord),
		keys:    make(map[string]*keyState),
	}
}

func (s *Store) Submit(change Change) (SubmitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateChange(change); err != nil {
		s.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
		return SubmitResult{}, err
	}
	if _, exists := s.records[change.ID]; exists {
		err := &RuleError{Reason: "duplicate change id"}
		s.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
		return SubmitResult{}, err
	}

	record := &changeRecord{Change: change}
	state := s.keyState(change.Subject, change.Label)
	if change.Kind == Revoke {
		target := state.byID[change.RevokesID]
		if target == nil {
			err := &RuleError{Reason: "revocation target is not in the same subject-label timeline"}
			s.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
			return SubmitResult{}, err
		}
		if target.withdrawn {
			err := &RuleError{Reason: "revocation target has been withdrawn"}
			s.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
			return SubmitResult{}, err
		}
		if change.SubmittedAt.Before(target.SubmittedAt) {
			err := &RuleError{Reason: "revocation was submitted before its target"}
			s.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
			return SubmitResult{}, err
		}
		if err := ensureNoRevocationCycle(state, record); err != nil {
			s.appendAudit("Submit", formatChange(change), "REJECTED: "+err.Error(), err.Error())
			return SubmitResult{}, err
		}
	}

	s.records[change.ID] = record
	state.records = append(state.records, record)
	state.byID[change.ID] = record
	if change.Kind == Revoke {
		state.dependents[change.RevokesID] = append(state.dependents[change.RevokesID], record)
		sortChanges(state.dependents[change.RevokesID])
	}
	sortChanges(state.records)
	rebuild(state)

	if !s.hasHorizon || change.SubmittedAt.Before(s.horizon) {
		s.horizon = change.SubmittedAt
		s.hasHorizon = true
	}

	result := SubmitResult{
		Accepted: true,
		OrderKey: change.EffectiveAt.Format(time.RFC3339Nano) + "|" +
			change.SubmittedAt.Format(time.RFC3339Nano) + "|" + change.ID,
	}
	s.appendAudit("Submit", formatChange(change), formatSubmitResult(result),
		"submission axis "+formatTime(change.SubmittedAt)+"; effective axis "+formatTime(change.EffectiveAt))
	return result, nil
}

func (s *Store) Withdraw(request WithdrawRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record := s.records[request.ID]
	if record == nil {
		err := &RuleError{Reason: "unknown change id"}
		s.appendAudit("Withdraw", formatWithdraw(request), "REJECTED: "+err.Error(), err.Error())
		return err
	}
	if !request.WithdrawnAt.Before(record.EffectiveAt) {
		err := &RuleError{Code: ErrWithdrawAlreadyActive, Reason: "change has already reached its effective time"}
		s.appendAudit("Withdraw", formatWithdraw(request), "REJECTED: "+err.Error(), err.Error())
		return err
	}
	if request.WithdrawnAt.Before(record.SubmittedAt) {
		err := &RuleError{Reason: "withdrawal time is before the change submission time"}
		s.appendAudit("Withdraw", formatWithdraw(request), "REJECTED: "+err.Error(), err.Error())
		return err
	}
	if record.withdrawn {
		err := &RuleError{Reason: "change is already withdrawn"}
		s.appendAudit("Withdraw", formatWithdraw(request), "REJECTED: "+err.Error(), err.Error())
		return err
	}

	record.withdrawn = true
	state := s.keyState(record.Subject, record.Label)
	rebuild(state)
	s.appendAudit("Withdraw", formatWithdraw(request), "ACCEPTED",
		"withdrawal precedes effective boundary "+formatTime(record.EffectiveAt))
	return nil
}

func (s *Store) Decide(query Query) (QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasHorizon && query.At.Before(s.horizon) {
		err := &RuleError{Code: ErrQueryBeforeHorizon, Reason: "query time precedes the earliest known record"}
		s.appendAudit("Decide", formatQuery(query), "REJECTED: "+err.Error(), err.Error())
		return QueryResult{}, err
	}

	state := s.keyState(query.Subject, query.Label)
	advance(state, query.At)

	activeAt := make(map[string]bool)
	var examined []string
	seen := make(map[string]bool)
	cutoff := 0
	for cutoff < len(state.events) && !state.events[cutoff].at.After(query.At) {
		event := state.events[cutoff]
		activeAt[event.change.ID] = event.active
		if !seen[event.change.ID] {
			seen[event.change.ID] = true
			examined = append(examined, event.change.ID)
		}
		cutoff++
	}

	var winner *changeRecord
	for id, active := range activeAt {
		if !active {
			continue
		}
		candidate := state.byID[id]
		if candidate.Kind != Put {
			continue
		}
		if winner == nil || changeOrder(candidate, winner) > 0 {
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
	s.appendAudit("Decide", formatQuery(query), formatQueryResult(result), basis)
	return result, nil
}

func (s *Store) keyState(subject, label string) *keyState {
	key := subject + "\x00" + label
	state := s.keys[key]
	if state == nil {
		state = &keyState{
			byID:         make(map[string]*changeRecord),
			dependents:   make(map[string][]*changeRecord),
			revokerCount: make(map[string]int),
			active:       make(map[string]bool),
		}
		s.keys[key] = state
	}
	return state
}

func validateChange(change Change) error {
	if change.EffectiveAt.Before(change.SubmittedAt) {
		return &RuleError{Code: ErrEffectiveBeforeSubmit, Reason: "effective time is earlier than submission time"}
	}
	if change.ID == "" || change.Subject == "" || change.Label == "" {
		return &RuleError{Reason: "id, subject, and label are required"}
	}
	switch change.Kind {
	case Put:
		if change.Decision != Allow && change.Decision != Deny {
			return &RuleError{Reason: "put change requires an allow or deny decision"}
		}
	case Revoke:
		if change.RevokesID == "" {
			return &RuleError{Reason: "revocation requires a target id"}
		}
	default:
		return &RuleError{Reason: "unknown change kind"}
	}
	return nil
}

func sortChanges(records []*changeRecord) {
	sort.Slice(records, func(i, j int) bool {
		return changeOrder(records[i], records[j]) < 0
	})
}

func changeOrder(left, right *changeRecord) int {
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

func ensureNoRevocationCycle(state *keyState, candidate *changeRecord) error {
	stack := map[string]bool{candidate.ID: true}
	done := make(map[string]bool)
	var visit func(record *changeRecord) bool
	visit = func(record *changeRecord) bool {
		if record == nil || record.Kind != Revoke {
			return false
		}
		target := state.byID[record.RevokesID]
		if record.RevokesID == candidate.ID {
			return true
		}
		if stack[record.RevokesID] {
			return true
		}
		if done[record.ID] || target == nil {
			return false
		}
		stack[record.RevokesID] = true
		found := visit(target)
		delete(stack, record.RevokesID)
		done[record.ID] = true
		return found
	}
	if visit(candidate) {
		return &RuleError{Reason: "revocation dependency cycle"}
	}
	return nil
}

func rebuild(state *keyState) {
	state.events = nil
	state.cursor = 0
	state.active = make(map[string]bool)
	state.revokerCount = make(map[string]int)
	state.dependents = make(map[string][]*changeRecord)
	sortChanges(state.records)
	for _, record := range state.records {
		if !record.withdrawn && record.Kind == Revoke {
			state.dependents[record.RevokesID] = append(state.dependents[record.RevokesID], record)
		}
	}
	for _, dependents := range state.dependents {
		sortChanges(dependents)
	}
}

func advance(state *keyState, at time.Time) {
	sortChanges(state.records)
	for state.cursor < len(state.records) && !state.records[state.cursor].EffectiveAt.After(at) {
		boundary := state.records[state.cursor].EffectiveAt
		groupStart := sort.Search(len(state.records), func(index int) bool {
			return !state.records[index].EffectiveAt.Before(boundary)
		})
		groupEnd := sort.Search(len(state.records), func(index int) bool {
			return state.records[index].EffectiveAt.After(boundary)
		})

		for _, record := range state.records[groupStart:groupEnd] {
			if !record.withdrawn {
				settleFrom(state, record, boundary)
			}
		}
		state.cursor = groupEnd
	}
}

func settleFrom(state *keyState, seed *changeRecord, at time.Time) {
	_ = seed
	for changed := true; changed; {
		changed = false
		var eligible []*changeRecord
		for _, record := range state.records {
			if record.withdrawn || state.active[record.ID] || record.EffectiveAt.After(at) || !canActivate(state, record) {
				continue
			}
			eligible = append(eligible, record)
		}
		sort.Slice(eligible, func(i, j int) bool {
			left, right := eligible[i], eligible[j]
			if !left.SubmittedAt.Equal(right.SubmittedAt) {
				return left.SubmittedAt.Before(right.SubmittedAt)
			}
			return left.ID < right.ID
		})
		for _, record := range eligible {
			if state.active[record.ID] || !canActivate(state, record) {
				continue
			}
			activate(state, record, at)
			if record.Kind == Revoke {
				if target := state.byID[record.RevokesID]; target != nil && state.active[target.ID] {
					deactivate(state, target, at)
				}
			}
			changed = true
		}
	}
}

func activate(state *keyState, record *changeRecord, at time.Time) {
	if state.active[record.ID] {
		return
	}
	state.active[record.ID] = true
	if record.Kind == Revoke {
		state.revokerCount[record.RevokesID]++
	}
	insertStateEvent(state, stateEvent{at: at, change: record, active: true, order: len(state.events)})
}

func deactivate(state *keyState, record *changeRecord, at time.Time) {
	if !state.active[record.ID] {
		return
	}
	state.active[record.ID] = false
	insertStateEvent(state, stateEvent{at: at, change: record, active: false, order: len(state.events)})

	if record.Kind != Revoke {
		return
	}
	state.revokerCount[record.RevokesID]--
}

func canActivate(state *keyState, record *changeRecord) bool {
	if record.withdrawn || state.active[record.ID] {
		return false
	}
	if state.revokerCount[record.ID] > 0 {
		return false
	}
	if record.Kind == Revoke {
		target := state.byID[record.RevokesID]
		return target != nil && !target.withdrawn && state.active[target.ID]
	}
	return true
}

func insertStateEvent(state *keyState, event stateEvent) {
	index := sort.Search(len(state.events), func(position int) bool {
		if !state.events[position].at.Equal(event.at) {
			return state.events[position].at.After(event.at)
		}
		return state.events[position].order > event.order
	})
	state.events = append(state.events, stateEvent{})
	copy(state.events[index+1:], state.events[index:])
	state.events[index] = event
}
