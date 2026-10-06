package ontology

import "sort"

func (m *NaiveModel) replayUntil(n int, _ bool) (map[string]*naiveOrder, map[string]*naiveVehicle, Time, error) {
	orders := map[string]*naiveOrder{}
	vehicles := map[string]*naiveVehicle{}
	waiting := []string{}
	clock := Time(0)
	var joinSeq int64
	for i := 0; i < n && i < len(m.events); i++ {
		ev := m.events[i]
		clock = ev.time
		switch ev.op {
		case "add_vehicle":
			vehicles[ev.vehicle.ID] = &naiveVehicle{in: ev.vehicle, at: ev.time, orders: map[string]struct{}{}}
		case "submit_order":
			in := ev.order
			joinSeq++
			ord := &naiveOrder{in: in, status: StatusWaiting, joinIndex: joinSeq}
			orders[in.ID] = ord
			if vid, ok := naiveChoose(vehicles, orders, ord, clock, m.cfg); ok {
				naiveAssign(vehicles, orders, waiting, ord, vid, clock, m.cfg)
			} else {
				waiting = append(waiting, in.ID)
			}
		case "update_position":
			v := vehicles[ev.id]
			for v.in.Position < ev.pos {
				plan := planRoute(v.in.Position, v.at, v.in.CurrentPassengers, naiveOrderList(v, orders), m.cfg)
				nextStop := Position(-1)
				var nextAt Time
				for _, stop := range plan.stops {
					if stop > v.in.Position && stop <= ev.pos {
						nextStop = stop
						nextAt = plan.arrival[stop]
						break
					}
				}
				if nextStop < 0 || nextAt > ev.time {
					break
				}
				stop := nextStop
				at := nextAt
				v.in.Position = stop
				v.at = at
				naiveComplete(v, orders, stop)
				waiting = naiveExpire(orders, waiting, at)
				waiting = naiveMatchAll(vehicles, orders, waiting, stop, at, m.cfg)
			}
			v.in.Position = ev.pos
			v.at = ev.time
			waiting = naiveExpire(orders, waiting, ev.time)
			waiting = naiveMatchAll(vehicles, orders, waiting, ev.pos, ev.time, m.cfg)
		case "cancel_order":
			ord := orders[ev.id]
			if ord.status == StatusMatched {
				v := vehicles[ord.vehicle]
				delete(v.orders, ev.id)
				naiveCancelReprice(v, orders, ord.joinIndex, m.cfg)
			} else if ord.status == StatusWaiting {
				waiting = removeNaiveWaiting(waiting, ev.id)
			}
			ord.status = StatusCancelled
			ord.vehicle = ""
			ord.payable = m.cfg.CancellationFee
		}
	}
	return orders, vehicles, clock, nil
}

func removeNaiveWaiting(waiting []string, id string) []string {
	out := make([]string, 0, len(waiting))
	for _, waitingID := range waiting {
		if waitingID != id {
			out = append(out, waitingID)
		}
	}
	return out
}

func (m *NaiveModel) lastClock() Time {
	_, _, clock, _ := m.replayUntil(len(m.events), false)
	return clock
}

func (m *NaiveModel) hasVehicle(id string) bool {
	_, vehicles, _, _ := m.replayUntil(len(m.events), false)
	return vehicles[id] != nil
}

func (m *NaiveModel) hasOrder(id string) bool {
	orders, _, _, _ := m.replayUntil(len(m.events), false)
	return orders[id] != nil
}

func (m *NaiveModel) currentOrder(id string) *naiveOrder {
	orders, _, _, _ := m.replayUntil(len(m.events), false)
	return orders[id]
}

func (m *NaiveModel) currentVehicle(id string) *naiveVehicle {
	_, vehicles, _, _ := m.replayUntil(len(m.events), false)
	return vehicles[id]
}

func naiveOrderList(v *naiveVehicle, orders map[string]*naiveOrder) []*order {
	out := make([]*order, 0, len(v.orders))
	for id := range v.orders {
		ord := orders[id]
		out = append(out, &order{input: ord.in, onboard: ord.onboard})
	}
	return out
}

func naiveChoose(vehicles map[string]*naiveVehicle, orders map[string]*naiveOrder, candidate *naiveOrder, at Time, cfg Config) (string, bool) {
	ids := make([]string, 0, len(vehicles))
	for id := range vehicles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	bestID := ""
	found := false
	var bestDelta Time
	cand := &order{input: candidate.in}
	for _, id := range ids {
		v := vehicles[id]
		base := planRoute(v.in.Position, at, v.in.CurrentPassengers, naiveOrderList(v, orders), cfg)
		plan, ok := canMerge(&vehicle{input: v.in, orderIDs: v.orders}, ordersMap(candidate, orders), cand, at, cfg)
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

func ordersMap(candidate *naiveOrder, orders map[string]*naiveOrder) map[string]*order {
	out := map[string]*order{}
	for id, ord := range orders {
		out[id] = &order{input: ord.in, onboard: ord.onboard}
	}
	out[candidate.in.ID] = &order{input: candidate.in}
	return out
}

func naiveAssign(vehicles map[string]*naiveVehicle, orders map[string]*naiveOrder, waiting []string, ord *naiveOrder, vid string, at Time, cfg Config) {
	v := vehicles[vid]
	ord.vehicle = vid
	ord.status = StatusMatched
	v.orders[ord.in.ID] = struct{}{}
	list := naiveOrderList(v, orders)
	raw := rawPrices(list, cfg)
	if ord.in.Pickup == v.in.Position {
		ord.status = StatusBoarded
		ord.onboard = true
		v.in.CurrentPassengers += ord.in.People
	}
	for id := range v.orders {
		target := orders[id]
		amount := minMoney(raw[id], Money(target.in.Dropoff-target.in.Pickup)*cfg.PricePerUnit)
		if id == ord.in.ID {
			target.cap = amount
			target.payable = amount
		} else if amount < target.payable {
			target.payable = amount
		}
	}
}

func naiveComplete(v *naiveVehicle, orders map[string]*naiveOrder, stop Position) {
	ids := make([]string, 0, len(v.orders))
	for id := range v.orders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ord := orders[id]
		if ord.in.Pickup == stop && ord.status == StatusMatched {
			ord.status = StatusBoarded
			ord.onboard = true
			v.in.CurrentPassengers += ord.in.People
		}
	}
	for _, id := range ids {
		ord := orders[id]
		if ord.in.Dropoff == stop {
			ord.status = StatusCompleted
			ord.onboard = false
			v.in.CurrentPassengers -= ord.in.People
			delete(v.orders, id)
		}
	}
}

func naiveExpire(orders map[string]*naiveOrder, waiting []string, at Time) []string {
	out := waiting[:0]
	for _, id := range waiting {
		if orders[id].in.LatestPickup < at {
			orders[id].status = StatusExpired
		} else {
			out = append(out, id)
		}
	}
	return out
}

func naiveMatchAll(vehicles map[string]*naiveVehicle, orders map[string]*naiveOrder, waiting []string, pos Position, at Time, cfg Config) []string {
	for {
		index := -1
		for i, id := range waiting {
			ord := orders[id]
			if ord.status == StatusWaiting && ord.in.LatestPickup >= at && ord.in.Pickup >= pos {
				if _, ok := naiveChoose(vehicles, orders, ord, at, cfg); ok {
					index = i
					break
				}
			}
		}
		if index < 0 {
			return waiting
		}
		id := waiting[index]
		waiting = append(waiting[:index], waiting[index+1:]...)
		vid, _ := naiveChoose(vehicles, orders, orders[id], at, cfg)
		naiveAssign(vehicles, orders, waiting, orders[id], vid, at, cfg)
	}
}

func naiveCancelReprice(v *naiveVehicle, orders map[string]*naiveOrder, joinIndex int64, cfg Config) {
	raw := rawPrices(naiveOrderList(v, orders), cfg)
	for id := range v.orders {
		ord := orders[id]
		if ord.joinIndex <= joinIndex {
			continue
		}
		ord.payable = minMoney(ord.payable, raw[id], ord.cap, Money(ord.in.Dropoff-ord.in.Pickup)*cfg.PricePerUnit)
	}
}
