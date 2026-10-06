package ontology

import "sort"

type routePlan struct {
	stops       []Position
	arrival     map[Position]Time
	delays      map[string]Time
	peopleAfter map[Position]int
	maxPeople   int
	totalDelay  Time
}

func validConfig(cfg Config) bool {
	return cfg.PricePerUnit > 0 && cfg.StopDuration >= 0 && cfg.TravelTimePerUnit > 0 &&
		cfg.MaxActiveOrders > 0 && cfg.CancellationFee >= 0
}

func validOrderInput(in OrderInput) bool {
	return in.ID != "" && in.People > 0 && in.Pickup < in.Dropoff &&
		in.MaxDelay >= 0 && in.LatestPickup >= in.BookTime
}

func planRoute(start Position, now Time, basePeople int, orders []*order, cfg Config) routePlan {
	stopSet := map[Position]struct{}{}
	for _, ord := range orders {
		if ord.input.Pickup > start {
			stopSet[ord.input.Pickup] = struct{}{}
		}
		if ord.input.Dropoff >= start {
			stopSet[ord.input.Dropoff] = struct{}{}
		}
	}
	stops := make([]Position, 0, len(stopSet))
	for pos := range stopSet {
		if pos >= start {
			stops = append(stops, pos)
		}
	}
	sort.Slice(stops, func(i, j int) bool { return stops[i] < stops[j] })

	arrival := map[Position]Time{}
	currentPosition := start
	at := now
	for _, stop := range stops {
		at += Time(stop-currentPosition) * cfg.TravelTimePerUnit
		arrival[stop] = at
		at += cfg.StopDuration
		currentPosition = stop
	}

	delays := make(map[string]Time, len(orders))
	var total Time
	for _, ord := range orders {
		var delay Time
		for _, stop := range stops {
			if stop > ord.input.Pickup && stop < ord.input.Dropoff {
				delay += cfg.StopDuration
			}
		}
		delays[ord.input.ID] = delay
		total += delay
	}

	peopleAfter := make(map[Position]int, len(stops))
	onboard := basePeople
	for _, ord := range orders {
		_ = ord
	}
	maxPeople := onboard
	for _, stop := range stops {
		for _, ord := range orders {
			if ord.input.Dropoff == stop {
				onboard -= ord.input.People
			}
		}
		for _, ord := range orders {
			if ord.input.Pickup == stop && stop >= start {
				onboard += ord.input.People
			}
		}
		peopleAfter[stop] = onboard
		if onboard > maxPeople {
			maxPeople = onboard
		}
	}
	return routePlan{stops: stops, arrival: arrival, delays: delays, peopleAfter: peopleAfter, maxPeople: maxPeople, totalDelay: total}
}

func canMerge(v *vehicle, allOrders map[string]*order, candidate *order, now Time, cfg Config) (routePlan, bool) {
	orders := make([]*order, 0, len(v.orderIDs)+1)
	for id := range v.orderIDs {
		orders = append(orders, allOrders[id])
	}
	orders = append(orders, candidate)

	plan := planRoute(v.input.Position, now, v.input.CurrentPassengers, orders, cfg)
	if candidate.input.Pickup < v.input.Position || plan.maxPeople > v.input.Seats ||
		len(orders) > cfg.MaxActiveOrders || plan.arrival[candidate.input.Pickup] > candidate.input.LatestPickup {
		return plan, false
	}
	for _, ord := range orders {
		if plan.delays[ord.input.ID] > ord.input.MaxDelay {
			return plan, false
		}
	}
	return plan, true
}
