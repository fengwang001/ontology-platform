package parking

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func TestNaiveComparison(t *testing.T) {
	zones, spots := testConfig()
	for seed := int64(1); seed <= 80; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			service, err := NewService(zones, spots)
			if err != nil {
				t.Fatal(err)
			}
			naive := newNaiveWorld(zones, spots)
			actions := generateActions(rng)
			known := map[string]bool{}
			var log []string
			for index, action := range actions {
				gotID, gotSpot, gotErr := runServiceAction(service, action)
				gotCode := ""
				if gotErr != nil {
					gotCode = string(errorCode(gotErr))
				}
				wantID, wantSecond := naive.apply(action)
				wantCode := ""
				wantSpot := ""
				if wantID != "" {
					wantSpot = wantSecond
				} else {
					wantCode = wantSecond
				}
				if gotCode != wantCode {
					t.Fatalf("action %d %s\nservice=%s(%s) naive=%s\nlog:\n%s", index, action, gotCode, gotErr, wantCode, joinLogs(log))
				}
				if gotCode == "" && gotID != wantID {
					t.Fatalf("id mismatch action=%d got=%s want=%s", index, gotID, wantID)
				}
				if gotCode == "" && action.kind == "reserve" && gotSpot != wantSpot {
					t.Fatalf("reserve mismatch action=%d got=%s want=%s", index, gotSpot, wantSpot)
				}
				if gotCode == "" && (action.kind == "reserve" || action.kind == "wait") {
					known[action.id] = true
				}
				log = append(log, fmt.Sprintf("#%03d input={%s} output={ok=%t code=%s id=%s spot=%s} basis=window/interval/first-fit/wait-order rules", index, action, gotCode == "", gotCode, gotID, gotSpot))
				assertWorldsAgree(t, service, naive, spots, log)
			}
			if testing.Verbose() {
				for _, line := range log {
					t.Log(line)
				}
			}
		})
	}
}

func generateActions(rng *rand.Rand) []naiveAction {
	zones, spots := testConfig()
	world := newNaiveWorld(zones, spots)
	var actions []naiveAction
	now := int64(0)
	vehicles := []string{"A", "B", "C", "D"}
	tokenSequence := 0
	for i := 0; i < 90; i++ {
		now += int64(rng.Intn(4))
		kind := []string{"reserve", "reserve", "wait", "checkin", "cancel", "leave"}[rng.Intn(6)]
		start := now + int64(rng.Intn(5))
		if rng.Intn(6) == 0 {
			start = now
		}
		end := start + int64(30+rng.Intn(180))
		action := naiveAction{kind: kind, at: now, vehicle: vehicles[rng.Intn(len(vehicles))], zone: "Z", start: start, end: end, charger: rng.Intn(6) == 0}
		if kind == "reserve" || kind == "wait" {
			action.id = fmt.Sprintf("new-%d", tokenSequence)
			tokenSequence++
			id, code := world.apply(action)
			if code != "" {
				action.kind = "reserve"
				action.charger = false
				action.start = now + 250 + int64(i)*30
				action.end = action.start + 60
				id, code = world.apply(action)
			}
			if code == "" {
				actions = append(actions, action)
			}
			_ = id
			continue
		}
		var candidates []string
		for id, r := range world.res {
			if (kind == "checkin" && r.status == nvReserved) || (kind == "cancel" && (r.status == nvReserved || r.status == nvWaiting)) || (kind == "leave" && r.status == nvOccupied) {
				candidates = append(candidates, id)
			}
		}
		if len(candidates) == 0 {
			action.kind = "reserve"
			action.id = fmt.Sprintf("new-%d", tokenSequence)
			tokenSequence++
			action.charger = false
			action.start = now + 300 + int64(i)*20
			action.end = action.start + 80
			world.apply(action)
			actions = append(actions, action)
			continue
		}
		sort.Strings(candidates)
		action.id = candidates[rng.Intn(len(candidates))]
		chosen := world.res[action.id]
		action.vehicle = chosen.vehicle
		action.charger = chosen.charger
		action.start = chosen.start
		action.end = chosen.end
		if kind == "checkin" {
			zone := world.zones[chosen.zone]
			windowStart := chosen.start - int64(zone.EarlyWindow)
			deadline := world.deadline(chosen)
			action.at = windowStart + rng.Int63n(deadline-windowStart+1)
			if action.at < now {
				action.at = now
			}
			now = action.at
		}
		if kind == "leave" && chosen.status == nvOccupied {
			action.at = chosen.checkedIn + rng.Int63n(180)
			if action.at < now {
				action.at = now
			}
			now = action.at
		}
		if kind == "cancel" && chosen.status == nvReserved {
			action.at = chosen.start + rng.Int63n(max64(1, int64(world.zones[chosen.zone].GracePeriod)))
			if action.at < now {
				action.at = now
			}
			now = action.at
		}
		if _, code := world.apply(action); code == "" {
			actions = append(actions, action)
		}
	}
	return actions
}

func runServiceAction(s *Service, action naiveAction) (string, string, error) {
	switch action.kind {
	case "reserve":
		result, err := s.Reserve(Time(action.at), ReservationRequest{ReservationID: action.id, ZoneID: action.zone, Start: Time(action.start), End: Time(action.end), Vehicle: action.vehicle, NeedCharger: action.charger})
		return result.ReservationID, result.SpotID, err
	case "wait":
		result, err := s.RegisterWait(Time(action.at), ReservationRequest{ReservationID: action.id, ZoneID: action.zone, Start: Time(action.start), End: Time(action.end), Vehicle: action.vehicle, NeedCharger: action.charger})
		return result.ReservationID, "waiting", err
	case "checkin":
		result, err := s.CheckIn(Time(action.at), action.id)
		return result.ReservationID, result.SpotID, err
	case "cancel":
		result, err := s.Cancel(Time(action.at), action.id)
		return result.ReservationID, "cancelled", err
	case "leave":
		result, err := s.Leave(Time(action.at), action.id)
		return result.ReservationID, "", err
	}
	return "", "", nil
}

func assertWorldsAgree(t *testing.T, service *Service, naive *naiveWorld, spots []Spot, log []string) {
	t.Helper()
	for _, spot := range spots {
		for at := int64(0); at < 430; at++ {
			got, err := service.SpotOccupantAt(spot.ID, Time(at))
			if err != nil {
				t.Fatal(err)
			}
			wantID, wantVehicle := naive.occ(spot.ID, at)
			if got.ReservationID != wantID || got.Vehicle != wantVehicle {
				t.Fatalf("spot=%s at=%d got=(%s,%s) want=(%s,%s)\n%s", spot.ID, at, got.ReservationID, got.Vehicle, wantID, wantVehicle, joinLogs(log))
			}
		}
	}
	for id, nr := range naive.res {
		fee, err := service.Fee(id)
		if err != nil {
			continue
		}
		base, overtime, cancelFee, comp := nr.base, nr.overtime, nr.cancel, nr.comp
		if fee.BaseFee != base || fee.OvertimeFee != overtime || fee.NoShowFee != cancelFee || fee.CompensationPaid != comp {
			t.Fatalf("fee mismatch %s got=(%d,%d,%d,%d) want=(%d,%d,%d,%d)\n%s", id, fee.BaseFee, fee.OvertimeFee, fee.NoShowFee, fee.CompensationPaid, base, overtime, cancelFee, comp, joinLogs(log))
		}
	}
}

func joinLogs(lines []string) string {
	if len(lines) > 18 {
		lines = lines[len(lines)-18:]
	}
	result := ""
	for _, line := range lines {
		result += line + "\n"
	}
	return result
}
