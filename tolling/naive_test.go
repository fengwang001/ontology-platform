package tolling

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

type naiveTripState struct {
	entry       GateRecord
	exit        GateRecord
	accepted    []GateRecord
	raw         Money
	collected   Money
	refunded    Money
	uncollected Money
}

type naiveReplayer struct {
	network *Network
	vehicle *Vehicle
	cfg     Config
	trips   map[string]*naiveTripState
	seen    map[string][]time.Time
	ledgers map[string]*monthLedger
}

func TestRandomReplayMatchesNaiveFullRecomputation(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			network := NewNetwork()
			gates := []string{"A", "B", "C", "D", "E"}
			for _, gate := range gates {
				if err := network.AddGate(gate); err != nil {
					t.Fatal(err)
				}
			}
			for _, from := range gates {
				for _, to := range gates {
					edge := Edge{From: from, To: to, Rates: map[string]Money{
						"car":   Money(1 + rng.Intn(9)),
						"truck": Money(7 + rng.Intn(10)),
					}}
					if err := network.AddEdge(edge); err != nil {
						t.Fatal(err)
					}
				}
			}
			cfg := Config{DuplicateWindow: 2 * time.Minute, LateWindow: 30 * time.Minute, MonthlyCap: 45, TimeZone: time.UTC}
			service := NewService(cfg, network)
			base := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
			vehicleID := "vehicle"
			if err := service.RegisterVehicle(Vehicle{ID: vehicleID, InitialClass: "car"}, base.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			naive := &naiveReplayer{network: network, vehicle: &Vehicle{ID: vehicleID, InitialClass: "car"}, cfg: cfg, trips: map[string]*naiveTripState{}, seen: map[string][]time.Time{}, ledgers: map[string]*monthLedger{}}
			for tripIndex := 0; tripIndex < 3; tripIndex++ {
				tripID := fmt.Sprintf("trip-%d", tripIndex)
				entryTime := base.Add(time.Duration(tripIndex*100+rng.Intn(5)) * time.Minute)
				exitTime := entryTime.Add(25 * time.Minute)
				entry := GateRecord{VehicleID: vehicleID, TripID: tripID, GateID: gates[rng.Intn(len(gates))], Kind: Entry, RecordedAt: entryTime}
				entryAt := entryTime.Add(time.Duration(1+rng.Intn(3)) * time.Minute)
				service.RegisterGate(entry, entryAt)
				naive.seen[dupKey(entry)] = append(naive.seen[dupKey(entry)], entry.RecordedAt)
				naive.trips[tripID] = &naiveTripState{entry: entry}
				t.Logf("seed=%d INPUT entry=%+v at=%v OUTPUT accepted basis=entry", seed, entry, entryAt)

				var early []GateRecord
				var late []GateRecord
				for minute := 2; minute < 24; minute += 3 {
					if rng.Intn(2) == 0 {
						continue
					}
					record := GateRecord{VehicleID: vehicleID, TripID: tripID, GateID: gates[rng.Intn(len(gates))], Kind: Intermediate, RecordedAt: entryTime.Add(time.Duration(minute) * time.Minute)}
					if naive.isDuplicate(record) {
						continue
					}
					naive.seen[dupKey(record)] = append(naive.seen[dupKey(record)], record.RecordedAt)
					if rng.Intn(2) == 0 {
						late = append(late, record)
					} else {
						early = append(early, record)
					}
				}
				clock := entryAt
				for index, record := range early {
					at := record.RecordedAt.Add(time.Duration(1+rng.Intn(2)) * time.Minute)
					if !at.After(clock) || !at.Before(exitTime) {
						at = clock.Add(time.Minute)
					}
					if !at.Before(exitTime) {
						at = exitTime.Add(-time.Duration(len(early)-index+1) * time.Minute)
					}
					result, err := service.RegisterGate(record, at)
					t.Logf("seed=%d INPUT early=%+v at=%v OUTPUT=%+v err=%v basis=record-time-ordered", seed, record, at, result, err)
					if err != nil && errorCode(err) != PathUnreachable {
						t.Fatal(err)
					}
					clock = at
				}
				exit := GateRecord{VehicleID: vehicleID, TripID: tripID, GateID: gates[rng.Intn(len(gates))], Kind: Exit, RecordedAt: exitTime}
				if naive.isDuplicate(exit) {
					exit.GateID = gates[(indexOfGate(gates, exit.GateID)+2)%len(gates)]
				}
				exitAt := clock.Add(2 * time.Minute)
				if !exitAt.After(exitTime) {
					exitAt = exitTime.Add(time.Minute)
				}
				result, err := service.RegisterGate(exit, exitAt)
				t.Logf("seed=%d INPUT exit=%+v at=%v OUTPUT=%+v err=%v basis=min-cost-lexicographic", seed, exit, exitAt, result, err)
				if err != nil {
					if errorCode(err) == PathUnreachable {
						continue
					}
					t.Fatal(err)
				}
				state := naive.trips[tripID]
				state.exit = exit
				state.accepted = early
				naive.recompute(t, tripID)
				clock = exitAt
				sortGateRecords(late)
				for index, record := range late {
					at := exitAt.Add(time.Duration(index+2) * time.Minute)
					result, err := service.RegisterGate(record, at)
					candidate := append(append([]GateRecord{}, state.accepted...), record)
					if _, _, naiveErr := naive.path(tripID, candidate); naiveErr == nil && at.Sub(exitTime) <= cfg.LateWindow {
						state.accepted = candidate
						naive.recompute(t, tripID)
					}
					t.Logf("seed=%d INPUT late=%+v at=%v OUTPUT=%+v err=%v basis=recorded-window-and-full-rebuild", seed, record, at, result, err)
					if err != nil && errorCode(err) != PathUnreachable {
						t.Fatal(err)
					}
					clock = at
				}
				naive.compare(t, seed, tripID, service)
			}
		})
	}
}

func dupKey(record GateRecord) string {
	return record.VehicleID + "\x00" + record.TripID + "\x00" + record.GateID
}

func (n *naiveReplayer) isDuplicate(record GateRecord) bool {
	for _, seen := range n.seen[dupKey(record)] {
		diff := record.RecordedAt.Sub(seen)
		if diff < 0 {
			diff = -diff
		}
		if diff < n.cfg.DuplicateWindow {
			return true
		}
	}
	return false
}

func (n *naiveReplayer) path(tripID string, records []GateRecord) ([]string, Money, error) {
	state := n.trips[tripID]
	anchors := []anchor{{gate: state.entry.GateID, class: n.vehicle.ClassAt(state.entry.RecordedAt)}}
	middle := append([]GateRecord{}, records...)
	sortGateRecords(middle)
	for _, record := range middle {
		if !record.RecordedAt.Before(state.entry.RecordedAt) && !record.RecordedAt.After(state.exit.RecordedAt) {
			anchors = append(anchors, anchor{gate: record.GateID, class: n.vehicle.ClassAt(record.RecordedAt)})
		}
	}
	anchors = append(anchors, anchor{gate: state.exit.GateID, class: n.vehicle.ClassAt(state.exit.RecordedAt)})
	return n.network.Reconstruct(anchors)
}

func (n *naiveReplayer) recompute(t *testing.T, tripID string) {
	t.Helper()
	state := n.trips[tripID]
	path, raw, err := n.path(tripID, state.accepted)
	if err != nil {
		t.Fatal(err)
	}
	key := state.entry.VehicleID + "\x00" + monthKey(state.exit.RecordedAt, n.cfg.TimeZone)
	ledger := n.ledgers[key]
	if ledger == nil {
		ledger = &monthLedger{}
		n.ledgers[key] = ledger
	}
	result := ledger.apply(raw, state.collected, state.uncollected, n.cfg.MonthlyCap)
	state.raw = raw
	state.collected += result.charge - result.refund
	state.refunded += result.refund
	state.uncollected = result.uncollected
	t.Logf("naive trip=%s path=%v raw=%d collected=%d refunded=%d uncollected=%d", tripID, path, raw, state.collected, state.refunded, state.uncollected)
}

func (n *naiveReplayer) compare(t *testing.T, seed int64, tripID string, service *Service) {
	t.Helper()
	view, err := service.Trip(tripID)
	if err != nil {
		t.Fatal(err)
	}
	state := n.trips[tripID]
	path, raw, _ := n.path(tripID, state.accepted)
	t.Logf("seed=%d COMPARE trip=%s servicePath=%v raw=%d collected=%d refunded=%d capped=%d naivePath=%v raw=%d collected=%d refunded=%d capped=%d", seed, tripID, view.Path, view.RawAmount, view.Collected, view.Refunded, view.CappedUncollected, path, raw, state.collected, state.refunded, state.uncollected)
	if fmt.Sprint(view.Path) != fmt.Sprint(path) || view.RawAmount != raw || view.Collected != state.collected || view.Refunded != state.refunded || view.CappedUncollected != state.uncollected {
		t.Fatalf("mismatch trip=%s service=%+v naivePath=%v raw=%d collected=%d refunded=%d capped=%d", tripID, view, path, raw, state.collected, state.refunded, state.uncollected)
	}
}

func sortGateRecords(records []GateRecord) {
	for i := 0; i < len(records); i++ {
		for j := i + 1; j < len(records); j++ {
			if records[j].RecordedAt.Before(records[i].RecordedAt) || records[j].RecordedAt.Equal(records[i].RecordedAt) && records[j].GateID < records[i].GateID {
				records[i], records[j] = records[j], records[i]
			}
		}
	}
}

func indexOfGate(gates []string, gate string) int {
	for i, item := range gates {
		if item == gate {
			return i
		}
	}
	return 0
}

func TestConcurrentReplayIsSerializable(t *testing.T) {
	for seed := int64(100); seed < 108; seed++ {
		rng := rand.New(rand.NewSource(seed))
		network := NewNetwork()
		gates := []string{"A", "B", "C", "D", "E"}
		for _, gate := range gates {
			if err := network.AddGate(gate); err != nil {
				t.Fatal(err)
			}
		}
		for _, from := range gates {
			for _, to := range gates {
				if err := network.AddEdge(Edge{From: from, To: to, Rates: map[string]Money{"car": Money(1 + rng.Intn(8))}}); err != nil {
					t.Fatal(err)
				}
			}
		}
		cfg := Config{DuplicateWindow: 2 * time.Minute, LateWindow: time.Hour, MonthlyCap: 200, TimeZone: time.UTC}
		s := NewService(cfg, network)
		base := time.Date(2026, 2, 1, 8, 0, 0, 0, time.UTC)
		for worker := 0; worker < 8; worker++ {
			if err := s.RegisterVehicle(Vehicle{ID: fmt.Sprintf("v-%d", worker), InitialClass: "car"}, base.Add(-2*time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
		entries := make(chan error, 8)
		for worker := 0; worker < 8; worker++ {
			worker := worker
			go func() {
				vehicleID := fmt.Sprintf("v-%d", worker)
				tripID := fmt.Sprintf("trip-%d", worker)
				entry := GateRecord{VehicleID: vehicleID, TripID: tripID, GateID: gates[worker%len(gates)], Kind: Entry, RecordedAt: base}
				_, err := s.RegisterGate(entry, base)
				entries <- err
			}()
		}
		for worker := 0; worker < 8; worker++ {
			if err := <-entries; err != nil {
				t.Fatal(err)
			}
		}
		exits := make(chan error, 8)
		for worker := 0; worker < 8; worker++ {
			worker := worker
			go func() {
				vehicleID := fmt.Sprintf("v-%d", worker)
				tripID := fmt.Sprintf("trip-%d", worker)
				exitAt := base.Add(20 * time.Minute)
				exit := GateRecord{VehicleID: vehicleID, TripID: tripID, GateID: gates[(worker+1)%len(gates)], Kind: Exit, RecordedAt: exitAt}
				if _, err := s.RegisterGate(exit, exitAt); err != nil {
					exits <- err
					return
				}
				_, err := s.Trip(tripID)
				exits <- err
			}()
		}
		for worker := 0; worker < 8; worker++ {
			if err := <-exits; err != nil {
				t.Fatal(err)
			}
		}
	}
}
