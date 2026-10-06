package staffing

import (
	"fmt"
	"math/rand/v2"
	"reflect"
)

type randomOp struct {
	kind      string
	day       int
	candidate string
	position  string
	offer     string
	approval  string
	number    int
	deadline  int
	start     int
	accept    bool
}

type naiveModel struct {
	clock      int
	cooldown   int
	grace      int
	candidates map[string]bool
	positions  map[string]Position
	offers     map[string]Offer
	exceptions map[string]ExceptionSnapshot
	lastExit   map[string]map[string]int
}

func newNaiveModel(cooldown, grace int) *naiveModel {
	return &naiveModel{
		cooldown:   cooldown,
		grace:      grace,
		candidates: map[string]bool{},
		positions:  map[string]Position{},
		offers:     map[string]Offer{},
		exceptions: map[string]ExceptionSnapshot{},
		lastExit:   map[string]map[string]int{},
	}
}

func naiveError(code ErrorCode) error { return &Error{Code: code, Index: -1} }

func (m *naiveModel) apply(op randomOp) error {
	switch op.kind {
	case "candidate":
		return m.addCandidate(op)
	case "position":
		return m.addPosition(op)
	case "freeze", "open":
		return m.setStatus(op)
	case "adjust":
		return m.adjust(op)
	case "grant":
		return m.grant(op)
	case "issue":
		return m.issue(op)
	case "respond", "withdraw", "cancel", "onboard", "terminate":
		return m.lifecycle(op)
	default:
		return naiveError(ErrInvalidArgument)
	}
}

func (m *naiveModel) addCandidate(op randomOp) error {
	if op.day < 0 || op.candidate == "" {
		return naiveError(ErrInvalidArgument)
	}
	if op.day < m.clock {
		return naiveError(ErrClockRolledBack)
	}
	if m.candidates[op.candidate] {
		return naiveError(ErrStatusNotAllowed)
	}
	m.candidates[op.candidate] = true
	m.clock = op.day
	return nil
}

func (m *naiveModel) addPosition(op randomOp) error {
	if op.day < 0 || op.position == "" || op.number < 0 {
		return naiveError(ErrInvalidArgument)
	}
	if op.day < m.clock {
		return naiveError(ErrClockRolledBack)
	}
	if _, exists := m.positions[op.position]; exists {
		return naiveError(ErrStatusNotAllowed)
	}
	m.positions[op.position] = Position{
		ID:        op.position,
		Level:     "L",
		MinSalary: 80,
		MaxSalary: 120,
		Total:     op.number,
		Status:    PositionOpen,
	}
	m.clock = op.day
	return nil
}

func (m *naiveModel) setStatus(op randomOp) error {
	if op.day < 0 || op.position == "" {
		return naiveError(ErrInvalidArgument)
	}
	if op.day < m.clock {
		return naiveError(ErrClockRolledBack)
	}
	position, ok := m.positions[op.position]
	if !ok {
		return naiveError(ErrNotFound)
	}
	if op.kind == "freeze" {
		position.Status = PositionFrozen
	} else {
		position.Status = PositionOpen
	}
	m.positions[op.position] = position
	m.clock = op.day
	return nil
}

func (m *naiveModel) adjust(op randomOp) error {
	if op.day < 0 || op.position == "" || op.number < 0 {
		return naiveError(ErrInvalidArgument)
	}
	if op.day < m.clock {
		return naiveError(ErrClockRolledBack)
	}
	position, ok := m.positions[op.position]
	if !ok {
		return naiveError(ErrNotFound)
	}
	if op.number < position.Occupied() {
		return naiveError(ErrHeadcountFull)
	}
	position.Total = op.number
	m.positions[op.position] = position
	m.clock = op.day
	return nil
}

func (m *naiveModel) grant(op randomOp) error {
	if op.day < 0 || op.position == "" || op.approval == "" || op.number < 0 {
		return naiveError(ErrInvalidArgument)
	}
	if op.day < m.clock {
		return naiveError(ErrClockRolledBack)
	}
	if _, ok := m.positions[op.position]; !ok {
		return naiveError(ErrNotFound)
	}
	if _, exists := m.exceptions[op.approval]; exists {
		return naiveError(ErrStatusNotAllowed)
	}
	m.exceptions[op.approval] = ExceptionSnapshot{
		ID:         op.approval,
		PositionID: op.position,
		FixedUses:  op.number,
		Used:       map[int]int{},
	}
	m.clock = op.day
	return nil
}

func (m *naiveModel) issue(op randomOp) error {
	if op.day < 0 || op.offer == "" || op.candidate == "" || op.position == "" || op.number < 0 || op.deadline < op.day {
		return naiveError(ErrInvalidArgument)
	}
	if op.day < m.clock {
		return naiveError(ErrClockRolledBack)
	}
	position, positionOK := m.positions[op.position]
	if !positionOK || !m.candidates[op.candidate] {
		return naiveError(ErrNotFound)
	}
	if _, exists := m.offers[op.offer]; exists {
		return naiveError(ErrStatusNotAllowed)
	}
	if position.Status == PositionFrozen {
		return naiveError(ErrPositionFrozen)
	}
	pendingExpiry := m.deferredExpirations(op.candidate, op.day)
	if position.Occupied()-len(pendingExpiry) >= position.Total {
		return naiveError(ErrHeadcountFull)
	}
	outOfBand := op.number < position.MinSalary || op.number > position.MaxSalary
	if outOfBand {
		grant, ok := m.exceptions[op.approval]
		if !ok || grant.PositionID != op.position || grant.Used[quarterOf(op.day)] >= grant.FixedUses {
			return naiveError(ErrSalaryBandWithoutApproval)
		}
	}
	if m.hasActiveOfferExcept(op.candidate, pendingExpiry) {
		return naiveError(ErrCandidateHasPendingOffer)
	}
	if day, ok := m.lastExit[op.candidate][op.position]; ok && op.day-day < m.cooldown {
		return naiveError(ErrCoolingDown)
	}
	approval := ""
	if outOfBand {
		approval = op.approval
		grant := m.exceptions[approval]
		grant.Used[quarterOf(op.day)]++
		m.exceptions[approval] = grant
	}
	for _, id := range pendingExpiry {
		offer := m.offers[id]
		offer.Status = OfferExpiredState
		m.releasePending(&offer)
		m.offers[id] = offer
	}
	position = m.positions[op.position]
	m.offers[op.offer] = Offer{
		ID:            op.offer,
		CandidateID:   op.candidate,
		PositionID:    op.position,
		Salary:        op.number,
		Deadline:      op.deadline,
		Status:        OfferPending,
		ExceptionUsed: approval,
	}
	position.Pending++
	m.positions[op.position] = position
	m.clock = op.day
	return nil
}

func (m *naiveModel) lifecycle(op randomOp) error {
	invalidStart := op.kind == "respond" && op.accept && op.start < op.day
	if op.day < 0 || op.offer == "" || invalidStart {
		return naiveError(ErrInvalidArgument)
	}
	if op.day < m.clock {
		return naiveError(ErrClockRolledBack)
	}
	offer, ok := m.offers[op.offer]
	if !ok {
		return naiveError(ErrNotFound)
	}
	if offer.Status == OfferPending && op.day > offer.Deadline {
		offer.Status = OfferExpiredState
		m.releasePending(&offer)
		m.offers[offer.ID] = offer
		m.clock = op.day
		return naiveError(ErrOfferExpired)
	}
	if offer.Status == OfferAccepted && op.day > offer.StartDay+m.grace {
		offer.Status = OfferAbandoned
		offer.AbandonedDay = op.day
		m.releasePending(&offer)
		m.recordExit(offer.CandidateID, offer.PositionID, op.day)
		m.offers[offer.ID] = offer
		m.clock = op.day
		return naiveError(ErrOfferExpired)
	}

	switch op.kind {
	case "respond":
		if offer.Status != OfferPending {
			return naiveError(ErrStatusNotAllowed)
		}
		if op.accept {
			offer.Status = OfferAccepted
			offer.StartDay = op.start
		} else {
			offer.Status = OfferRejected
			offer.RejectedDay = op.day
			m.releasePending(&offer)
			m.recordExit(offer.CandidateID, offer.PositionID, op.day)
		}
	case "withdraw":
		if offer.Status != OfferPending {
			return naiveError(ErrStatusNotAllowed)
		}
		offer.Status = OfferWithdrawn
		m.releasePending(&offer)
	case "cancel":
		if offer.Status != OfferAccepted {
			return naiveError(ErrStatusNotAllowed)
		}
		offer.Status = OfferCanceled
		offer.CanceledDay = op.day
		m.releasePending(&offer)
	case "onboard":
		if offer.Status != OfferAccepted || op.day < offer.StartDay {
			return naiveError(ErrStatusNotAllowed)
		}
		offer.Status = OfferOnboarded
		offer.OnboardedDay = op.day
		position := m.positions[offer.PositionID]
		position.Pending--
		position.OnDuty++
		m.positions[offer.PositionID] = position
	case "terminate":
		if offer.Status != OfferOnboarded {
			return naiveError(ErrStatusNotAllowed)
		}
		offer.Status = OfferTerminated
		offer.TerminatedDay = op.day
		position := m.positions[offer.PositionID]
		position.OnDuty--
		m.positions[offer.PositionID] = position
	}
	m.offers[offer.ID] = offer
	m.clock = op.day
	return nil
}

func (m *naiveModel) releasePending(offer *Offer) {
	position := m.positions[offer.PositionID]
	position.Pending--
	m.positions[offer.PositionID] = position
}

func (m *naiveModel) recordExit(candidateID, positionID string, day int) {
	if m.lastExit[candidateID] == nil {
		m.lastExit[candidateID] = map[string]int{}
	}
	m.lastExit[candidateID][positionID] = day
}

func (m *naiveModel) deferredExpirations(candidateID string, day int) []string {
	ids := []string{}
	for id, offer := range m.offers {
		if offer.CandidateID == candidateID && offer.Status == OfferPending && day > offer.Deadline {
			ids = append(ids, id)
		}
	}
	return ids
}

func (m *naiveModel) hasActiveOfferExcept(candidateID string, expiring []string) bool {
	for _, offer := range m.offers {
		isExpiring := false
		for _, id := range expiring {
			if offer.ID == id {
				isExpiring = true
			}
		}
		if !isExpiring && offer.CandidateID == candidateID && (offer.Status == OfferPending || offer.Status == OfferAccepted) {
			return true
		}
	}
	return false
}

var _ = fmt.Sprintf
var _ = reflect.DeepEqual
var _ = rand.NewPCG
