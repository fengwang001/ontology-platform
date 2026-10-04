package gate_test

import (
	"fmt"
	"math/rand"

	"ontology/gate"
)

func randomOp(rng *rand.Rand, model *naiveModel, now int64) op {
	switch rng.Intn(11) {
	case 0, 1, 2:
		model.nextOID++
		acct := []string{"A1", "A2", "A3"}[rng.Intn(3)]
		side := []gate.Side{gate.Long, gate.Short}[rng.Intn(2)]
		offset := gate.Open
		if rng.Intn(5) == 0 {
			offset = gate.Close
		}
		return op{
			kind: "order", now: now, oid: fmt.Sprintf("o%d", model.nextOID),
			acct: acct, sym: "S", side: side, offset: offset, qty: int64(1 + rng.Intn(25)),
		}
	case 3, 4:
		if oid, ok := anyOpenOrder(model, rng); ok {
			return op{kind: "fill", now: now, oid: oid, qty: int64(1 + rng.Intn(int(model.orders[oid].remain)+1))}
		}
	case 5:
		if oid, ok := anyOpenOrder(model, rng); ok {
			return op{kind: "cancel", now: now, oid: oid}
		}
	case 6:
		return op{
			kind: "hedge", now: now, acct: []string{"A1", "A2", "A3"}[rng.Intn(3)],
			sym: "S", side: gate.Long, hedge: int64(rng.Intn(30)),
		}
	case 7:
		return op{kind: "limit", now: now, sym: "S", la: int64(rng.Intn(61)), lg: int64(rng.Intn(101)), day: int64(rng.Intn(151))}
	case 8:
		return op{kind: "reset", now: now}
	case 9:
		return op{kind: "register", now: now, acct: "A1", group: "G"}
	default:
		model.nextOID++
		return op{kind: "fill", now: now, oid: fmt.Sprintf("missing-%d", model.nextOID), qty: 1, want: gate.ErrNotFound}
	}
	return op{kind: "reset", now: now}
}

func anyOpenOrder(model *naiveModel, rng *rand.Rand) (string, bool) {
	ids := make([]string, 0)
	for id, entry := range model.orders {
		if !entry.finished && entry.remain > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "", false
	}
	return ids[rng.Intn(len(ids))], true
}
