package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type naiveBooking struct {
	id       int64
	owner    int64
	slot     int64
	size     int64
	tier     int
	sequence int64
}

type naiveState struct {
	capacity    int64
	windowSize  int64
	maxOversell int64
	rate        int64
	lastSettled int64
	nextSeq     int64
	bookings    map[int64]*naiveBooking
	window      []SettlementRecord
	k           map[int64]int64
}

func newNaive(capacity, windowSize, maxOversell, rate int64) *naiveState {
	return &naiveState{
		capacity:    capacity,
		windowSize:  windowSize,
		maxOversell: maxOversell,
		rate:        rate,
		lastSettled: -1,
		nextSeq:     1,
		bookings:    make(map[int64]*naiveBooking),
		k:           make(map[int64]int64),
	}
}

func (n *naiveState) oversell() int64 {
	var totalReserved, totalArrived int64
	for _, record := range n.window {
		totalReserved += record.Reservations
		totalArrived += record.Arrivals
	}
	if totalReserved == 0 {
		return 0
	}
	raw := (totalReserved - totalArrived) * 10_000 / totalReserved
	return min(raw, n.maxOversell)
}

func (n *naiveState) limit() int64 {
	return n.capacity * (10_000 + n.oversell()) / 10_000
}

func (n *naiveState) validBook(id, owner, slot, size int64, tier int) bool {
	return id >= 0 && id <= 1_000_000 &&
		owner >= 0 && owner <= 1_000_000 &&
		slot >= 0 && slot <= 1_000_000 &&
		size >= 1 && size <= 1_000_000 &&
		tier >= 0 && tier <= 2
}

func (n *naiveState) book(id, owner, slot, size int64, tier int) error {
	if !n.validBook(id, owner, slot, size, tier) {
		return ErrInvalidArgument
	}
	if _, exists := n.bookings[id]; exists {
		return ErrDuplicateID
	}
	if slot <= n.lastSettled {
		return ErrSlotSettled
	}
	var reserved int64
	for _, booked := range n.bookings {
		if booked.slot == slot {
			reserved += booked.size
		}
	}
	if reserved+size > n.limit() {
		return ErrCapacityLimit
	}
	n.bookings[id] = &naiveBooking{
		id:       id,
		owner:    owner,
		slot:     slot,
		size:     size,
		tier:     tier,
		sequence: n.nextSeq,
	}
	n.nextSeq++
	return nil
}

func (n *naiveState) settle(slot int64, arrivals []Arrival) (SettleResult, error) {
	if slot < 0 || slot > 1_000_000 {
		return SettleResult{}, ErrInvalidArgument
	}
	seen := make(map[int64]int64)
	for _, arrival := range arrivals {
		if arrival.ID < 0 || arrival.ID > 1_000_000 {
			return SettleResult{}, ErrInvalidArgument
		}
		if _, duplicated := seen[arrival.ID]; duplicated {
			return SettleResult{}, ErrInvalidArgument
		}
		booked, exists := n.bookings[arrival.ID]
		if !exists || booked.slot != slot || arrival.Amount < 0 || arrival.Amount > booked.size {
			return SettleResult{}, ErrInvalidArgument
		}
		seen[arrival.ID] = arrival.Amount
	}
	if slot <= n.lastSettled {
		return SettleResult{}, ErrSlotRollback
	}

	type state struct {
		booked  *naiveBooking
		arrived int64
		bumped  int64
	}
	var all []state
	var reservedTotal, arrivedTotal int64
	for _, booked := range n.bookings {
		if booked.slot == slot {
			reservedTotal += booked.size
			amount := seen[booked.id]
			arrivedTotal += amount
			all = append(all, state{booked: booked, arrived: amount})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].booked.sequence < all[j].booked.sequence })

	var candidates []*state
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

	record := SettlementRecord{Reservations: reservedTotal, Arrivals: arrivedTotal}
	n.window = append(n.window, record)
	if int64(len(n.window)) > n.windowSize {
		n.window = n.window[int64(len(n.window))-n.windowSize:]
	}

	excess := max(arrivedTotal-n.capacity, 0)
	remaining := excess
	totalCompensation := int64(0)
	bumpedOwners := make(map[int64]struct{})
	for _, candidate := range candidates {
		if remaining == 0 {
			break
		}
		bumped := min(candidate.arrived, remaining)
		candidate.bumped = bumped
		remaining -= bumped
		multiplier := int64(1) + min(n.k[candidate.booked.owner], 3)
		totalCompensation += bumped * n.rate * multiplier
		bumpedOwners[candidate.booked.owner] = struct{}{}
	}
	for owner := range bumpedOwners {
		n.k[owner]++
	}
	n.lastSettled = slot

	result := SettleResult{
		Slot:              slot,
		ArrivedTotal:      arrivedTotal,
		BumpedTotal:       excess,
		Excess:            excess,
		TotalCompensation: totalCompensation,
		WindowRecord:      record,
	}
	for _, item := range all {
		result.ServedTotal += item.arrived - item.bumped
		result.Records = append(result.Records, ReservationSettlement{
			ID:           item.booked.id,
			Owner:        item.booked.owner,
			Slot:         item.booked.slot,
			Sequence:     item.booked.sequence,
			Tier:         item.booked.tier,
			Size:         item.booked.size,
			Arrived:      item.arrived,
			Served:       item.arrived - item.bumped,
			Bumped:       item.bumped,
			Compensation: item.bumped * n.rate * (1 + min(n.k[item.booked.owner]-1, 3)),
		})
	}
	return result, nil
}

func TestRandomSequencesAgainstNaiveSimulation(t *testing.T) {
	for trial := 0; trial < 2000; trial++ {
		rng := rand.New(rand.NewSource(int64(12450000 + trial)))
		capacity := int64(1 + rng.Intn(30))
		windowSize := int64(1 + rng.Intn(5))
		maxOversell := int64(rng.Intn(10_001))
		rate := int64(rng.Intn(20))
		actual, err := NewCapacityBook(capacity, windowSize, maxOversell, rate)
		if err != nil {
			t.Fatal(err)
		}
		expected := newNaive(capacity, windowSize, maxOversell, rate)
		nextID := int64(0)
		trace := make([]string, 0, 34)

		for step := 0; step < 34; step++ {
			slot := int64(rng.Intn(8))
			if rng.Intn(10) < 6 {
				id := nextID
				nextID++
				owner := int64(rng.Intn(5))
				size := int64(1 + rng.Intn(12))
				tier := rng.Intn(3)
				if rng.Intn(12) == 0 {
					id = int64(rng.Intn(int(nextID) + 1))
				}
				input := fmt.Sprintf("trial=%d step=%d Book(%d,%d,%d,%d,%d)", trial, step, id, owner, slot, size, tier)
				gotErr := actual.Book(id, owner, slot, size, tier)
				wantErr := expected.book(id, owner, slot, size, tier)
				line := fmt.Sprintf("input=%s output=%v expected=%v judgement=compare sentinel error, sequence and all state", input, gotErr, wantErr)
				trace = append(trace, line)
				t.Log(line)
				if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
					for _, line := range trace {
						t.Log(line)
					}
					t.Fatalf("%s: error %v, want %v", input, gotErr, wantErr)
				}
				if gotErr == nil {
					if actual.nextSeq != expected.nextSeq {
						for _, line := range trace {
							t.Log(line)
						}
						t.Fatalf("%s: sequence %d, want %d", input, actual.nextSeq, expected.nextSeq)
					}
				}
				continue
			}

			var arrivals []Arrival
			for _, booked := range expected.bookings {
				if booked.slot == slot && rng.Intn(2) == 0 {
					arrivals = append(arrivals, Arrival{
						ID:     booked.id,
						Amount: int64(rng.Intn(int(booked.size) + 1)),
					})
				}
			}
			sort.Slice(arrivals, func(i, j int) bool { return arrivals[i].ID < arrivals[j].ID })
			sent := append([]Arrival(nil), arrivals...)
			input := fmt.Sprintf("trial=%d step=%d Settle(%d,%v)", trial, step, slot, sent)
			gotResult, gotErr := actual.Settle(slot, sent)
			wantResult, wantErr := expected.settle(slot, arrivals)
			line := fmt.Sprintf("input=%s output=%+v,%v expected=%+v,%v judgement=compare settlement result and error", input, gotResult, gotErr, wantResult, wantErr)
			trace = append(trace, line)
			t.Log(line)
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				for _, line := range trace {
					t.Log(line)
				}
				t.Fatalf("%s: settle error %v, want %v", input, gotErr, wantErr)
			}
			if gotErr == nil {
				sort.Slice(gotResult.Records, func(i, j int) bool { return gotResult.Records[i].Sequence < gotResult.Records[j].Sequence })
				if len(gotResult.Records) == 0 {
					gotResult.Records = nil
				}
				if len(wantResult.Records) == 0 {
					wantResult.Records = nil
				}
				if !reflect.DeepEqual(gotResult, wantResult) {
					for _, line := range trace {
						t.Log(line)
					}
					t.Fatalf("%s:\nresult=%+v\nwant=%+v", input, gotResult, wantResult)
				}
			}

			if actual.lastSettled != expected.lastSettled ||
				!reflect.DeepEqual(actual.window, expected.window) ||
				actual.oversellLocked() != expected.oversell() ||
				actual.limitLocked() != expected.limit() ||
				actual.nextSeq != expected.nextSeq {
				for _, line := range trace {
					t.Log(line)
				}
				t.Fatalf("%s: state mismatch actual=%+v expected=%+v", input, actual, expected)
			}
		}

		for owner := int64(0); owner < 5; owner++ {
			if actual.BumpCount(owner) != expected.k[owner] {
				for _, line := range trace {
					t.Log(line)
				}
				t.Fatalf("trial=%d owner=%d k=%d want=%d", trial, owner, actual.BumpCount(owner), expected.k[owner])
			}
		}
	}
}
