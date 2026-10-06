package ontology

import "sort"

func soloPrice(ord *order, cfg Config) Money {
	return Money(ord.input.Dropoff-ord.input.Pickup) * cfg.PricePerUnit
}

func rawPrices(orders []*order, cfg Config) map[string]Money {
	raw := make(map[string]Money, len(orders))
	if len(orders) == 0 {
		return raw
	}
	stopSet := map[Position]struct{}{}
	for _, ord := range orders {
		stopSet[ord.input.Pickup] = struct{}{}
		stopSet[ord.input.Dropoff] = struct{}{}
	}
	stops := make([]Position, 0, len(stopSet))
	for pos := range stopSet {
		stops = append(stops, pos)
	}
	sort.Slice(stops, func(i, j int) bool { return stops[i] < stops[j] })

	for i := 0; i+1 < len(stops); i++ {
		left, right := stops[i], stops[i+1]
		riders := make([]*order, 0)
		people := 0
		for _, ord := range orders {
			if ord.input.Pickup <= left && right <= ord.input.Dropoff {
				riders = append(riders, ord)
				people += ord.input.People
			}
		}
		if len(riders) == 0 {
			continue
		}
		sort.Slice(riders, func(i, j int) bool {
			if riders[i].input.BookTime != riders[j].input.BookTime {
				return riders[i].input.BookTime < riders[j].input.BookTime
			}
			return riders[i].input.ID < riders[j].input.ID
		})
		base := Money(right-left) * cfg.PricePerUnit
		earliest := riders[0]
		otherAmount := Money(0)
		for _, ord := range riders[1:] {
			amount := (base * Money(ord.input.People)) / Money(people)
			raw[ord.input.ID] += amount
			otherAmount += amount
		}
		raw[earliest.input.ID] += base - otherAmount
	}
	return raw
}

func vehicleOrders(v *vehicle, all map[string]*order) []*order {
	orders := make([]*order, 0, len(v.orderIDs))
	for id := range v.orderIDs {
		if ord := all[id]; ord != nil {
			orders = append(orders, ord)
		}
	}
	return orders
}

func applyJoinPrices(v *vehicle, all map[string]*order, joined map[*order]bool, cfg Config) {
	raw := rawPrices(vehicleOrders(v, all), cfg)
	for id := range v.orderIDs {
		ord := all[id]
		amount := minMoney(raw[id], soloPrice(ord, cfg))
		if joined[ord] {
			ord.view.Cap = amount
			ord.view.Payable = amount
		} else if amount < ord.view.Payable {
			ord.view.Payable = amount
		}
	}
}

func applyCancelRepricing(v *vehicle, all map[string]*order, cancelJoinIndex int64, cfg Config) {
	raw := rawPrices(vehicleOrders(v, all), cfg)
	for id := range v.orderIDs {
		ord := all[id]
		if ord.joinIndex <= cancelJoinIndex {
			continue
		}
		ord.view.Payable = minMoney(ord.view.Payable, raw[id], ord.view.Cap, soloPrice(ord, cfg))
	}
}

func minMoney(values ...Money) Money {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}
