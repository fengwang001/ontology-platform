package ontology

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidConfig   = errors.New("invalid capacity book configuration")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrDuplicateID     = errors.New("duplicate booking id")
	ErrSlotSettled     = errors.New("slot has already been settled")
	ErrCapacityLimit   = errors.New("booking exceeds slot capacity limit")
	ErrSlotRollback    = errors.New("settlement slot must be greater than last settled slot")
)

type SettlementRecord struct {
	Reservations int64
	Arrivals     int64
}

type Arrival struct {
	ID     int64
	Amount int64
}

type ReservationSettlement struct {
	ID           int64
	Owner        int64
	Slot         int64
	Sequence     int64
	Tier         int
	Size         int64
	Arrived      int64
	Served       int64
	Bumped       int64
	Compensation int64
}

type SettleResult struct {
	Slot              int64
	Records           []ReservationSettlement
	ArrivedTotal      int64
	ServedTotal       int64
	BumpedTotal       int64
	Excess            int64
	TotalCompensation int64
	WindowRecord      SettlementRecord
}

type reservation struct {
	id       int64
	owner    int64
	slot     int64
	size     int64
	tier     int
	sequence int64
}

type CapacityBook struct {
	mu          sync.RWMutex
	capacity    int64
	windowSize  int64
	maxOversell int64
	rate        int64
	lastSettled int64
	window      []SettlementRecord
	nextSeq     int64
	byID        map[int64]*reservation
	bySlot      map[int64]map[int64]*reservation
	booked      map[int64]int64
	bumpCounts  map[int64]int64
}

func NewCapacityBook(capacity, windowSize, maxOversell, rate int64) (*CapacityBook, error) {
	if capacity < 1 || capacity > 1_000_000 ||
		windowSize < 1 || windowSize > 64 ||
		maxOversell < 0 || maxOversell > 10_000 ||
		rate < 0 || rate > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	return &CapacityBook{
		capacity:    capacity,
		windowSize:  windowSize,
		maxOversell: maxOversell,
		rate:        rate,
		lastSettled: -1,
		nextSeq:     1,
		byID:        make(map[int64]*reservation),
		bySlot:      make(map[int64]map[int64]*reservation),
		booked:      make(map[int64]int64),
		bumpCounts:  make(map[int64]int64),
	}, nil
}

func (b *CapacityBook) Book(id, owner, slot, size int64, tier int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id < 0 || id > 1_000_000 ||
		owner < 0 || owner > 1_000_000 ||
		slot < 0 || slot > 1_000_000 ||
		size < 1 || size > 1_000_000 ||
		tier < 0 || tier > 2 {
		return ErrInvalidArgument
	}
	if _, exists := b.byID[id]; exists {
		return ErrDuplicateID
	}
	if slot <= b.lastSettled {
		return ErrSlotSettled
	}

	limit := b.limitLocked()
	if b.booked[slot]+size > limit {
		return ErrCapacityLimit
	}

	booked := &reservation{
		id:       id,
		owner:    owner,
		slot:     slot,
		size:     size,
		tier:     tier,
		sequence: b.nextSeq,
	}
	b.nextSeq++
	b.byID[id] = booked
	if b.bySlot[slot] == nil {
		b.bySlot[slot] = make(map[int64]*reservation)
	}
	b.bySlot[slot][id] = booked
	b.booked[slot] += size
	return nil
}

func (b *CapacityBook) Settle(slot int64, arrivals []Arrival) (SettleResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if slot < 0 || slot > 1_000_000 {
		return SettleResult{}, ErrInvalidArgument
	}

	slotReservations := b.bySlot[slot]
	arrived := make(map[int64]int64, len(arrivals))
	for _, arrival := range arrivals {
		if arrival.ID < 0 || arrival.ID > 1_000_000 {
			return SettleResult{}, ErrInvalidArgument
		}
		if _, duplicated := arrived[arrival.ID]; duplicated {
			return SettleResult{}, ErrInvalidArgument
		}
		booked, exists := slotReservations[arrival.ID]
		if !exists || arrival.Amount < 0 || arrival.Amount > booked.size {
			return SettleResult{}, ErrInvalidArgument
		}
		arrived[arrival.ID] = arrival.Amount
	}

	if slot <= b.lastSettled {
		return SettleResult{}, ErrSlotRollback
	}

	reservedTotal := b.booked[slot]
	var arrivalTotal int64
	type settlementState struct {
		booked  *reservation
		arrived int64
		bumped  int64
	}

	all := make([]settlementState, 0, len(slotReservations))
	for _, booked := range slotReservations {
		arrivalAmount := arrived[booked.id]
		arrivalTotal += arrivalAmount
		state := settlementState{
			booked:  booked,
			arrived: arrivalAmount,
		}
		all = append(all, state)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].booked.sequence < all[j].booked.sequence
	})
	candidates := make([]*settlementState, 0, len(all))
	for i := range all {
		if all[i].arrived > 0 {
			candidates = append(candidates, &all[i])
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].booked.tier != candidates[j].booked.tier {
			return candidates[i].booked.tier > candidates[j].booked.tier
		}
		return candidates[i].booked.sequence > candidates[j].booked.sequence
	})

	record := SettlementRecord{
		Reservations: reservedTotal,
		Arrivals:     arrivalTotal,
	}
	b.window = append(b.window, record)
	if int64(len(b.window)) > b.windowSize {
		b.window = b.window[int64(len(b.window))-b.windowSize:]
	}

	var bumpedTotal, totalCompensation int64
	bumpedOwners := make(map[int64]struct{})
	excess := int64(0)
	if arrivalTotal > b.capacity {
		excess = arrivalTotal - b.capacity
	}
	remaining := excess
	for _, candidate := range candidates {
		if remaining == 0 {
			break
		}
		bumped := min(candidate.arrived, remaining)
		candidate.bumped = bumped
		remaining -= bumped
		bumpedTotal += bumped

		previousBumps := b.bumpCounts[candidate.booked.owner]
		multiplier := int64(1) + min(previousBumps, 3)
		totalCompensation += bumped * b.rate * multiplier
		bumpedOwners[candidate.booked.owner] = struct{}{}
	}

	for owner := range bumpedOwners {
		b.bumpCounts[owner]++
	}
	b.lastSettled = slot

	records := make([]ReservationSettlement, 0, len(all))
	var servedTotal int64
	for _, state := range all {
		served := state.arrived - state.bumped
		servedTotal += served
		records = append(records, ReservationSettlement{
			ID:           state.booked.id,
			Owner:        state.booked.owner,
			Slot:         state.booked.slot,
			Sequence:     state.booked.sequence,
			Tier:         state.booked.tier,
			Size:         state.booked.size,
			Arrived:      state.arrived,
			Served:       served,
			Bumped:       state.bumped,
			Compensation: state.bumped * b.rate * (1 + min(b.bumpCounts[state.booked.owner]-1, 3)),
		})
	}

	return SettleResult{
		Slot:              slot,
		Records:           records,
		ArrivedTotal:      arrivalTotal,
		ServedTotal:       servedTotal,
		BumpedTotal:       bumpedTotal,
		Excess:            excess,
		TotalCompensation: totalCompensation,
		WindowRecord:      record,
	}, nil
}

func (b *CapacityBook) CurrentOversell() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.oversellLocked()
}

func (b *CapacityBook) CurrentLimit() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.limitLocked()
}

func (b *CapacityBook) LastSettled() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastSettled
}

func (b *CapacityBook) Window() []SettlementRecord {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]SettlementRecord(nil), b.window...)
}

func (b *CapacityBook) BumpCount(owner int64) int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.bumpCounts[owner]
}

func (b *CapacityBook) oversellLocked() int64 {
	var reservedTotal, arrivalTotal int64
	for _, record := range b.window {
		reservedTotal += record.Reservations
		arrivalTotal += record.Arrivals
	}
	if reservedTotal == 0 {
		return 0
	}
	absent := reservedTotal - arrivalTotal
	oversell := absent * 10_000 / reservedTotal
	return min(oversell, b.maxOversell)
}

func (b *CapacityBook) limitLocked() int64 {
	return b.capacity * (10_000 + b.oversellLocked()) / 10_000
}
