package ontology

import (
	"context"
	"sort"
)

func (s *Service) UpdatePosition(_ context.Context, vehicleID string, at Time, pos Position) (result PositionResult, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reason := "rejected before state change"
	defer func() {
		s.record("update_position", at, map[string]any{"vehicle": vehicleID, "position": pos}, result, err, reason)
	}()
	if vehicleID == "" || pos < 0 {
		return result, ErrInvalidArgument
	}
	if at < s.clock {
		return result, ErrClockRollback
	}
	v := s.vehicles[vehicleID]
	if v == nil {
		return result, ErrVehicleNotFound
	}
	if pos < v.input.Position {
		return result, ErrPositionRollback
	}
	s.clock = at
	result.VehicleID = vehicleID
	for v.input.Position < pos {
		plan := planRoute(v.input.Position, v.lastTime, v.input.CurrentPassengers, vehicleOrders(v, s.orders), s.cfg)
		nextStop := Position(-1)
		var nextAt Time
		for _, stop := range plan.stops {
			if stop > v.input.Position && stop <= pos {
				nextStop = stop
				nextAt = plan.arrival[stop]
				break
			}
		}
		if nextStop < 0 {
			break
		}
		if nextAt > at {
			break
		}
		stop := nextStop
		stopAt := nextAt
		v.input.Position = stop
		v.lastTime = stopAt
		result.Completed = append(result.Completed, s.completeAt(v, stop)...)
		s.expireWaiting(stopAt, &result)
		result.Matched = append(result.Matched, s.matchWaitingAt(v, stop, stopAt)...)
	}
	v.input.Position = pos
	v.lastTime = at
	s.expireWaiting(at, &result)
	result.Matched = append(result.Matched, s.matchWaitingAt(v, pos, at)...)
	result.Position = pos
	reason = "advanced along corridor and processed current-stop events"
	return result, nil
}

func (s *Service) findVehicle(candidate *order, at Time) (string, bool) {
	ids := sortedVehicleIDs(s.vehicles)
	bestID := ""
	bestDelta := Time(0)
	found := false
	for _, id := range ids {
		v := s.vehicles[id]
		base := planRoute(v.input.Position, at, v.input.CurrentPassengers, vehicleOrders(v, s.orders), s.cfg)
		plan, ok := canMerge(v, s.orders, candidate, at, s.cfg)
		if !ok {
			continue
		}
		delta := plan.totalDelay - base.totalDelay
		if !found || delta < bestDelta || delta == bestDelta && id < bestID {
			bestID, bestDelta, found = id, delta, true
		}
	}
	return bestID, found
}

func (s *Service) assign(ord *order, vehicleID string, at Time) {
	v := s.vehicles[vehicleID]
	s.seq++
	ord.joinIndex = s.seq
	ord.view.Status = StatusMatched
	ord.view.VehicleID = vehicleID
	v.orderIDs[ord.input.ID] = struct{}{}
	joined := map[*order]bool{ord: true}
	if ord.input.Pickup == v.input.Position {
		ord.view.Status = StatusBoarded
		ord.onboard = true
		v.input.CurrentPassengers += ord.input.People
	}
	plan := planRoute(v.input.Position, at, v.input.CurrentPassengers, vehicleOrders(v, s.orders), s.cfg)
	_ = plan
	applyJoinPrices(v, s.orders, joined, s.cfg)
}

func (s *Service) completeAt(v *vehicle, stop Position) []string {
	completed := []string{}
	ids := sortedOrderIDs(v.orderIDs)
	for _, id := range ids {
		ord := s.orders[id]
		if ord.input.Pickup == stop && ord.view.Status == StatusMatched {
			ord.view.Status = StatusBoarded
			ord.onboard = true
			v.input.CurrentPassengers += ord.input.People
		}
	}
	for _, id := range ids {
		ord := s.orders[id]
		if ord.input.Dropoff == stop {
			completed = append(completed, id)
			ord.view.Status = StatusCompleted
			ord.onboard = false
			v.input.CurrentPassengers -= ord.input.People
			delete(v.orderIDs, id)
		}
	}
	return completed
}

func (s *Service) expireWaiting(at Time, result *PositionResult) {
	kept := s.waiting[:0]
	for _, id := range s.waiting {
		ord := s.orders[id]
		if ord.input.LatestPickup < at {
			ord.view.Status = StatusExpired
			result.Expired = append(result.Expired, id)
		} else {
			kept = append(kept, id)
		}
	}
	s.waiting = kept
}

func (s *Service) matchWaitingAt(v *vehicle, pos Position, at Time) []string {
	matched := []string{}
	for {
		matchIndex := -1
		for i, id := range s.waiting {
			ord := s.orders[id]
			if ord.input.LatestPickup >= at && ord.input.Pickup >= pos {
				if _, ok := s.findVehicle(ord, at); ok {
					matchIndex = i
					break
				}
			}
		}
		if matchIndex < 0 {
			return matched
		}
		id := s.waiting[matchIndex]
		s.waiting = append(s.waiting[:matchIndex], s.waiting[matchIndex+1:]...)
		ord := s.orders[id]
		vid, _ := s.findVehicle(ord, at)
		s.assign(ord, vid, at)
		matched = append(matched, id)
	}
}

func (s *Service) removeWaiting(id string) {
	for i, waitingID := range s.waiting {
		if waitingID == id {
			s.waiting = append(s.waiting[:i], s.waiting[i+1:]...)
			return
		}
	}
}

func (s *Service) nextIndex() int64 {
	s.seq++
	return s.seq
}

func (s *Service) record(op string, at Time, input map[string]any, output any, err error, reason string) {
	entry := LogEntry{Seq: s.seq, Op: op, Time: at, Input: input, Output: output, Reason: reason}
	if err != nil {
		entry.Err = err.Error()
	}
	s.log.Log(entry)
}

func sortedVehicleIDs(values map[string]*vehicle) []string {
	out := make([]string, 0, len(values))
	for id := range values {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func sortedOrderIDs(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for id := range values {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
