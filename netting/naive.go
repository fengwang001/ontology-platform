package netting

import "sort"

// naiveSettle is a from-scratch reference implementation of the settlement
// rules, intentionally written in the most literal round-by-round style so it
// can cross-check Engine.settle. It does not share any helper code with the
// engine. Simultaneous controls whether all defaulters in a round are detected
// together (true, the specified semantics) or one at a time in id order
// (false, used by tests to prove the two interpretations can differ).
func naiveSettle(parties map[string]int64, input []Obligation, simultaneous bool) (rounds [][]string, revoked []string, positions []Position, instructions []Instruction) {
	// Defensive copy: the simulation mutates its own tables only.
	alive := make(map[string]bool, len(input))
	obls := make([]Obligation, len(input))
	copy(obls, input)
	for _, o := range obls {
		alive[o.OID] = true
	}

	defaulted := map[string]bool{}

	computeNets := func() map[string]int64 {
		net := map[string]int64{}
		for _, o := range obls {
			if !alive[o.OID] {
				continue
			}
			net[o.From] -= o.Amount
			net[o.To] += o.Amount
		}
		return net
	}

	revoke := func(defaulter string) {
		defaulted[defaulter] = true
		for _, o := range obls {
			if alive[o.OID] && (o.From == defaulter || o.To == defaulter || defaulted[o.From] || defaulted[o.To]) {
				alive[o.OID] = false
				revoked = append(revoked, o.OID)
			}
		}
	}

	for {
		net := computeNets()
		var violators []string
		for id, n := range net {
			if n < 0 && -n > parties[id] {
				violators = append(violators, id)
			}
		}
		sort.Strings(violators)
		if len(violators) == 0 {
			break
		}
		if simultaneous {
			round := append([]string(nil), violators...)
			rounds = append(rounds, round)
			for _, id := range violators {
				revoke(id)
			}
		} else {
			// One-at-a-time variant: revoke the smallest id, then recompute;
			// each id still occupies its own detection "round".
			id := violators[0]
			rounds = append(rounds, []string{id})
			revoke(id)
		}
	}

	sort.Strings(revoked)

	finalNet := computeNets()
	ids := make([]string, 0, len(parties))
	for id := range parties {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		positions = append(positions, Position{ID: id, Net: finalNet[id]})
	}

	type bucket struct {
		id  string
		amt int64
	}
	var payers, payees []bucket
	for _, p := range positions {
		if p.Net < 0 {
			payers = append(payers, bucket{p.ID, -p.Net})
		} else if p.Net > 0 {
			payees = append(payees, bucket{p.ID, p.Net})
		}
	}
	sortBucket := func(s []bucket) {
		sort.Slice(s, func(i, j int) bool {
			if s[i].amt != s[j].amt {
				return s[i].amt > s[j].amt
			}
			return s[i].id < s[j].id
		})
	}
	sortBucket(payers)
	sortBucket(payees)

	pi, qi := 0, 0
	for pi < len(payers) && qi < len(payees) {
		pay := payers[pi].amt
		if payees[qi].amt < pay {
			pay = payees[qi].amt
		}
		instructions = append(instructions, Instruction{
			Payer:  payers[pi].id,
			Payee:  payees[qi].id,
			Amount: pay,
		})
		payers[pi].amt -= pay
		payees[qi].amt -= pay
		if payers[pi].amt == 0 {
			pi++
		}
		if payees[qi].amt == 0 {
			qi++
		}
	}

	return rounds, dedupeSorted(revoked), positions, instructions
}

func dedupeSorted(s []string) []string {
	sort.Strings(s)
	out := s[:0]
	for i, v := range s {
		if i > 0 && s[i-1] == v {
			continue
		}
		out = append(out, v)
	}
	return out
}
