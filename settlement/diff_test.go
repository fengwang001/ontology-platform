package settlement

import (
	"fmt"
	"math/rand"
	"testing"
)

// Op kinds.
const (
	opAddMerchant = iota
	opTxn
	opSettle
)

type op struct {
	kind   int
	now    Day
	mid    string
	cfg    MerchantConfig
	tx     Transaction
	day    Day
	reason string
	valid  bool
}

const diffMaxDay = 24

// genScenario builds a deterministic, legal (monotone-now) random timeline in
// which transactions and settlements interleave. A minority of deliberately
// invalid ops (duplicate id, future date, sealed backfill, unknown merchant,
// clock rollback, non-business day, duplicate settle) is mixed in; these must
// be rejected and leave no trace.
func genScenario(seed int64) []op {
	rng := rand.New(rand.NewSource(seed))
	ops := make([]op, 0, 400)

	nMerch := 1 + rng.Intn(3)
	type mc struct{ n, bps, h int }
	conf := make([]mc, nMerch)
	now := Day(-10)
	for i := 0; i < nMerch; i++ {
		c := mc{
			n:   1 + rng.Intn(3),
			bps: []int{0, 100, 500, 1000, 2500, 10000}[rng.Intn(6)],
			h:   1 + rng.Intn(4),
		}
		conf[i] = c
		ops = append(ops, op{kind: opAddMerchant, now: now, mid: fmt.Sprintf("M%d", i),
			cfg:    MerchantConfig{c.n, c.bps, c.h},
			reason: "add merchant", valid: true})
	}

	frontier := make([]Day, nMerch)
	settled := make([]bool, nMerch)
	nextID := 0

	for now = 0; now <= diffMaxDay; now++ {
		// Settle first, then add same-day transactions. The sealed frontier
		// after settling business day t is the N-th business day on/before t,
		// so transactions dated >= that frontier remain legal and will be picked
		// up by the next settlement. This keeps the stream legal under both
		// catch-up and day-by-day replay.
		settledToday := make([]bool, nMerch)
		for mi := 0; mi < nMerch; mi++ {
			if rng.Intn(10) < 5 {
				ops = append(ops, op{kind: opSettle, now: now, mid: fmt.Sprintf("M%d", mi),
					day: now, reason: "settle", valid: true})
				sealed := now - Day(conf[mi].n) + 1
				if sealed >= 0 {
					frontier[mi] = sealed
				}
				settled[mi] = true
				settledToday[mi] = true
			}
		}

		nTx := rng.Intn(5)
		for k := 0; k < nTx; k++ {
			mi := rng.Intn(nMerch)
			lo := 0
			sealed := now - Day(conf[mi].n) + 1
			if settled[mi] && sealed >= 0 {
				lo = int(sealed)
			}
			if lo > int(now) {
				continue
			}
			d := lo + rng.Intn(int(now)-lo+1)
			amt := Amount(rng.Intn(201) - 130)
			id := fmt.Sprintf("t%d", nextID)
			nextID++
			ops = append(ops, op{kind: opTxn, now: now, mid: fmt.Sprintf("M%d", mi),
				tx:     Transaction{ID: id, Day: Day(d), Amount: amt},
				reason: "valid txn", valid: true})
		}

		if rng.Intn(3) == 0 {
			mi := rng.Intn(nMerch)
			switch rng.Intn(7) {
			case 0:
				// duplicate id: reuse the first generated id
				ops = append(ops, op{kind: opTxn, now: now, mid: fmt.Sprintf("M%d", mi),
					tx:     Transaction{ID: "t0", Day: now, Amount: 5},
					reason: "duplicate id", valid: false})
			case 1:
				ops = append(ops, op{kind: opTxn, now: now, mid: fmt.Sprintf("M%d", mi),
					tx:     Transaction{ID: fmt.Sprintf("bad%d", nextID), Day: now + 1, Amount: 5},
					reason: "future date", valid: false})
				nextID++
			case 2:
				if settled[mi] && frontier[mi] > 0 {
					ops = append(ops, op{kind: opTxn, now: now, mid: fmt.Sprintf("M%d", mi),
						tx:     Transaction{ID: fmt.Sprintf("bad%d", nextID), Day: frontier[mi] - 1, Amount: 5},
						reason: "sealed backfill", valid: false})
					nextID++
				}
			case 3:
				ops = append(ops, op{kind: opTxn, now: now, mid: "ZZ",
					tx:     Transaction{ID: fmt.Sprintf("bad%d", nextID), Day: now, Amount: 5},
					reason: "unknown merchant", valid: false})
				nextID++
			case 4:
				ops = append(ops, op{kind: opTxn, now: now - 1, mid: fmt.Sprintf("M%d", mi),
					tx:     Transaction{ID: fmt.Sprintf("bad%d", nextID), Day: now - 1, Amount: 5},
					reason: "clock rollback", valid: false})
				nextID++
			case 5:
				// Duplicate settlement on the business day settled earlier today.
				if settledToday[mi] {
					ops = append(ops, op{kind: opSettle, now: now, mid: fmt.Sprintf("M%d", mi),
						day: now, reason: "duplicate settle", valid: false})
				}
			case 6:
				// Non-business day: coordinate outside the configured calendar.
				ops = append(ops, op{kind: opSettle, now: now, mid: fmt.Sprintf("M%d", mi),
					day: diffMaxDay + 100, reason: "non-business day", valid: false})
			}
		}
	}

	// Settle every merchant through the final day so final state is fully
	// determined and easy to compare.
	for mi := 0; mi < nMerch; mi++ {
		ops = append(ops, op{kind: opSettle, now: diffMaxDay + 1, mid: fmt.Sprintf("M%d", mi),
			day: diffMaxDay, reason: "final catch up", valid: true})
	}
	return ops
}

// replayOps runs the stream on the production engine on the full business
// calendar 0..diffMaxDay. Catch-up settlements (one call spanning many
// business days) are used; their internal per-day determination is logged.
func replayOps(t *testing.T, ops []op) []MerchantState {
	t.Helper()
	s := mustSystem(t, days(0, diffMaxDay))

	merchantOrder := make([]string, 0)

	for _, o := range ops {
		switch o.kind {
		case opAddMerchant:
			err := s.AddMerchant(o.now, o.mid, o.cfg)
			t.Logf("AddMerchant now=%d %s cfg=%+v -> %v", o.now, o.mid, o.cfg, err)
			if err == nil {
				merchantOrder = append(merchantOrder, o.mid)
			}
		case opTxn:
			err := s.AddTransaction(o.now, o.mid, o.tx)
			t.Logf("Txn now=%d m=%s id=%s day=%d amt=%d (%s wantValid=%v) -> %v",
				o.now, o.mid, o.tx.ID, o.tx.Day, o.tx.Amount,
				o.reason, o.valid, err)
		case opSettle:
			p, details, err := s.Settle(o.now, o.mid, o.day)
			t.Logf("Settle m=%s day=%d -> err=%v payouts=%v", o.mid, o.day, err, p)
			for _, dt := range details {
				t.Logf("    basis day=%d boundary=%d extracted=%d carryIn=%d net=%d release=%d retain=%d consumed=%d carryOut=%d payout=%d",
					dt.Day, dt.BoundaryDay, dt.ExtractedTxns, dt.NegativeCarry, dt.SettleableNet,
					dt.Released, dt.NewRetained, dt.Consumed, dt.CarryAfter, dt.Payout)
			}
		}
	}

	out := make([]MerchantState, 0, len(merchantOrder))
	for _, id := range merchantOrder {
		st, err := s.State(id)
		if err == nil {
			out = append(out, st)
		}
	}
	return out
}

func assertStatesEqual(t *testing.T, a, b []MerchantState) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("merchant count %d vs %d", len(a), len(b))
	}
	for i := range a {
		sa, sb := a[i], b[i]
		if sa.ID != sb.ID {
			t.Fatalf("id mismatch %s vs %s", sa.ID, sb.ID)
		}
		if len(sa.Payouts) != len(sb.Payouts) {
			t.Fatalf("%s payouts len %d vs %d:\na=%+v\nb=%+v", sa.ID, len(sa.Payouts), len(sb.Payouts), sa, sb)
		}
		for k := range sa.Payouts {
			if sa.Payouts[k] != sb.Payouts[k] {
				t.Fatalf("%s payout[%d] %+v vs %+v", sa.ID, k, sa.Payouts[k], sb.Payouts[k])
			}
		}
		if sa.TotalPayout != sb.TotalPayout || sa.ReserveBalance != sb.ReserveBalance ||
			sa.NegativeCarry != sb.NegativeCarry || sa.LastSettledDay != sb.LastSettledDay ||
			sa.Settled != sb.Settled {
			t.Fatalf("%s state mismatch:\na=%+v\nb=%+v", sa.ID, sa, sb)
		}
		if len(sa.Batches) != len(sb.Batches) {
			t.Fatalf("%s batch count %d vs %d:\na=%+v\nb=%+v", sa.ID, len(sa.Batches), len(sb.Batches), sa.Batches, sb.Batches)
		}
		for k := range sa.Batches {
			if sa.Batches[k] != sb.Batches[k] {
				t.Fatalf("%s batch[%d] %+v vs %+v", sa.ID, k, sa.Batches[k], sb.Batches[k])
			}
		}
	}
}
