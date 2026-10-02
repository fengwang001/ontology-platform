package paymentcard

import (
	"errors"
	"sort"
	"sync"
)

var ErrInvalidParameters = errors.New("invalid parameters")

type Outcome string

const (
	OutcomeOK                Outcome = "ok"
	OutcomeAccepted          Outcome = "accepted"
	OutcomeInvalidParameters Outcome = "invalid_parameters"
	OutcomeFrozen            Outcome = "frozen"
	OutcomeDuplicate         Outcome = "duplicate"
	OutcomeOutOfOrder        Outcome = "out_of_order"
	OutcomeImpossible        Outcome = "impossible_travel"
	OutcomeCardNotFound      Outcome = "card_not_found"
	OutcomeNotFrozen         Outcome = "not_frozen"
)

type Transaction struct {
	T int64
	X int64
	Y int64
}

type CardSnapshot struct {
	HasAnchor       bool
	T               int64
	X               int64
	Y               int64
	Rejections      []int64
	Frozen          bool
	HasTravelWindow bool
	TravelFrom      int64
	TravelTo        int64
}

type Detector struct {
	mu              sync.RWMutex
	maxSpeed        int64
	freezeLimit     int64
	rejectionWindow int64
	cards           map[string]*cardState
}

type anchor struct {
	t int64
	x int64
	y int64
}

type cardState struct {
	anchor     *anchor
	rejections []int64
	frozen     bool
	travel     *travelWindow
}

type travelWindow struct {
	from int64
	to   int64
}

func NewDetector(maxSpeedKmh, freezeThreshold, rejectionWindowSeconds int64) (*Detector, error) {
	if maxSpeedKmh < 1 || maxSpeedKmh > 1_000_000 ||
		freezeThreshold < 1 || freezeThreshold > 100 ||
		rejectionWindowSeconds < 1 || rejectionWindowSeconds > 1_000_000_000_000 {
		return nil, ErrInvalidParameters
	}

	return &Detector{
		maxSpeed:        maxSpeedKmh,
		freezeLimit:     freezeThreshold,
		rejectionWindow: rejectionWindowSeconds,
		cards:           make(map[string]*cardState),
	}, nil
}

func (d *Detector) Check(card []byte, t, x, y int64) Outcome {
	if !validCard(card) || !validTransaction(t, x, y) {
		return OutcomeInvalidParameters
	}

	key := string(card)
	d.mu.Lock()
	defer d.mu.Unlock()

	state := d.card(key)
	return d.checkLocked(state, Transaction{T: t, X: x, Y: y})
}

func (d *Detector) CheckBatch(card []byte, transactions []Transaction) []Outcome {
	if !validCard(card) || len(transactions) < 1 || len(transactions) > 1000 {
		return invalidBatch(len(transactions))
	}
	for _, transaction := range transactions {
		if !validTransaction(transaction.T, transaction.X, transaction.Y) {
			return invalidBatch(len(transactions))
		}
	}

	order := make([]int, len(transactions))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return transactions[order[i]].T < transactions[order[j]].T
	})

	results := make([]Outcome, len(transactions))
	key := string(card)
	d.mu.Lock()
	defer d.mu.Unlock()

	state := d.card(key)
	for _, index := range order {
		results[index] = d.checkLocked(state, transactions[index])
	}
	return results
}

func (d *Detector) Declare(card []byte, from, to int64) Outcome {
	if !validCard(card) || from < 0 || from >= to || to > 10_000_000_000_000 {
		return OutcomeInvalidParameters
	}

	key := string(card)
	d.mu.Lock()
	defer d.mu.Unlock()

	d.card(key).travel = &travelWindow{from: from, to: to}
	return OutcomeOK
}

func (d *Detector) Unfreeze(card []byte) Outcome {
	if !validCard(card) {
		return OutcomeInvalidParameters
	}

	key := string(card)
	d.mu.Lock()
	defer d.mu.Unlock()

	state, exists := d.cards[key]
	if !exists {
		return OutcomeCardNotFound
	}
	if !state.frozen {
		return OutcomeNotFrozen
	}

	state.frozen = false
	state.rejections = nil
	return OutcomeOK
}

func (d *Detector) Snapshot(card []byte) (CardSnapshot, bool) {
	if !validCard(card) {
		return CardSnapshot{}, false
	}

	key := string(card)
	d.mu.RLock()
	defer d.mu.RUnlock()

	state, exists := d.cards[key]
	if !exists {
		return CardSnapshot{}, false
	}

	snapshot := CardSnapshot{
		HasAnchor:  state.anchor != nil,
		Rejections: append([]int64(nil), state.rejections...),
		Frozen:     state.frozen,
	}
	if state.anchor != nil {
		snapshot.T = state.anchor.t
		snapshot.X = state.anchor.x
		snapshot.Y = state.anchor.y
	}
	if state.travel != nil {
		snapshot.HasTravelWindow = true
		snapshot.TravelFrom = state.travel.from
		snapshot.TravelTo = state.travel.to
	}
	return snapshot, true
}

func (d *Detector) card(key string) *cardState {
	state := d.cards[key]
	if state == nil {
		state = &cardState{}
		d.cards[key] = state
	}
	return state
}

func (d *Detector) checkLocked(state *cardState, transaction Transaction) Outcome {
	if state.frozen {
		return OutcomeFrozen
	}

	if state.anchor != nil && state.anchor.t == transaction.T && state.anchor.x == transaction.X && state.anchor.y == transaction.Y {
		return OutcomeDuplicate
	}
	if state.anchor != nil && transaction.T < state.anchor.t {
		return OutcomeOutOfOrder
	}
	if state.anchor == nil {
		state.anchor = &anchor{t: transaction.T, x: transaction.X, y: transaction.Y}
		return OutcomeAccepted
	}
	if state.travel != nil && state.travel.from <= transaction.T && transaction.T < state.travel.to {
		state.anchor = &anchor{t: transaction.T, x: transaction.X, y: transaction.Y}
		return OutcomeAccepted
	}

	distance := absInt64(transaction.X-state.anchor.x) + absInt64(transaction.Y-state.anchor.y)
	elapsed := transaction.T - state.anchor.t
	if distance*3600 <= d.maxSpeed*elapsed {
		state.anchor = &anchor{t: transaction.T, x: transaction.X, y: transaction.Y}
		return OutcomeAccepted
	}

	state.rejections = append(state.rejections, transaction.T)
	count := int64(1)
	cutoff := transaction.T - d.rejectionWindow
	for _, rejectionTime := range state.rejections[:len(state.rejections)-1] {
		if rejectionTime > cutoff {
			count++
		}
	}
	if count >= d.freezeLimit {
		state.frozen = true
	}
	return OutcomeImpossible
}

func validCard(card []byte) bool {
	return len(card) > 0
}

func validTransaction(t, x, y int64) bool {
	return t >= 0 && t <= 1_000_000_000_000 &&
		x >= -1_000_000_000 && x <= 1_000_000_000 &&
		y >= -1_000_000_000 && y <= 1_000_000_000
}

func invalidBatch(size int) []Outcome {
	if size <= 0 {
		return []Outcome{}
	}
	results := make([]Outcome, size)
	for i := range results {
		results[i] = OutcomeInvalidParameters
	}
	return results
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
