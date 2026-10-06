package chargeback_test

import (
	"flag"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	cb "ontology/chargeback"
	"ontology/chargeback/naive"
)

var verboseDiff = flag.Bool("diffverbose", false, "print every differential step (input/output/basis)")

// diffConfig mirrors windows into the two packages.
type diffConfig struct {
	c cb.Config
	n naive.Config
}

func mkDiffConfig() diffConfig {
	c := cb.Config{
		FraudWindowDays:       8,
		NotReceivedWindowDays: 12,
		DuplicateWindowDays:   10,
		DuplicateRangeDays:    4,
		DefenseDays:           3,
		ReviewDays:            2,
		ArbitrationFee:        7,
	}
	return diffConfig{
		c: c,
		n: naive.Config{
			FraudWindowDays: c.FraudWindowDays, NotReceivedWindowDays: c.NotReceivedWindowDays,
			DuplicateWindowDays: c.DuplicateWindowDays, DuplicateRangeDays: c.DuplicateRangeDays,
			DefenseDays: c.DefenseDays, ReviewDays: c.ReviewDays, ArbitrationFee: c.ArbitrationFee,
		},
	}
}

type txnInfo struct {
	id       string
	card     string
	merchant string
	amount   int64
	settle   int
}

type logger struct{ b strings.Builder }

func (l *logger) logf(f string, a ...any) { l.b.WriteString(fmt.Sprintf(f+"\n", a...)) }

func (l *logger) flush(t *testing.T) {
	t.Helper()
	t.Logf("\n%s", l.b.String())
}

// runDifferential executes one random sequence against both engines and
// compares the error of every operation plus every money/status query.
func runDifferential(t *testing.T, rng *rand.Rand, steps int) {
	t.Helper()
	cfg := mkDiffConfig()
	sys := cb.New(cfg.c)
	nm := naive.New(cfg.n)
	lg := &logger{}

	var txns []txnInfo
	merchants := map[string]bool{}
	var caseIDs []string
	creditLedger := map[int]int64{}   // day -> total credits posted that day
	merchFeeLedger := map[int]int64{} // day -> merchant-paid fees that day
	issuerFeeLedger := map[int]int64{}
	txnIDs := func() []string {
		out := make([]string, 0, len(txns))
		for _, x := range txns {
			out = append(out, x.id)
		}
		return out
	}
	prefixAt := func(led map[int]int64, day int) int64 {
		var s int64
		for d, v := range led {
			if d <= day {
				s += v
			}
		}
		return s
	}
	creditsBefore := func(day int) int64 { return prefixAt(creditLedger, day) }
	totalFeesBefore := func(day int) int64 {
		return prefixAt(merchFeeLedger, day) + prefixAt(issuerFeeLedger, day)
	}

	now := 0
	failf := func(format string, a ...any) {
		lg.flush(t)
		t.Fatalf(format, a...)
	}
	cmpErr := func(step string, e1, e2 error) {
		t.Helper()
		b1 := errName(e1)
		b2 := errName(e2)
		if b1 != b2 {
			failf("%s: error mismatch production=%q naive=%q", step, b1, b2)
		}
	}

	for i := 0; i < steps; i++ {
		// Time mostly advances, occasionally stalls (same-day), never jumps
		// backward as an accepted clock.
		switch rng.Intn(10) {
		case 0, 1, 2:
			// stay on now (same-day operations)
		default:
			now += rng.Intn(4)
		}
		k := rng.Intn(14)
		switch {
		case k == 0:
			id := fmt.Sprintf("T%03d", len(txns))
			card := fmt.Sprintf("card%d", rng.Intn(3))
			merch := []string{"mA", "mB"}[rng.Intn(2)]
			amt := int64(20 + rng.Intn(100))
			settle := now
			if rng.Intn(4) == 0 && now > 2 {
				settle = now - rng.Intn(3)
			}
			info := txnInfo{id, card, merch, amt, settle}
			nt := naive.Transaction{ID: id, CardID: card, MerchantID: merch, Amount: amt, SettlementDay: settle}
			ct := cb.Transaction{ID: id, CardID: card, MerchantID: merch, Amount: amt, SettlementDay: settle}
			e1 := sys.RegisterTransaction(now, ct)
			e2 := nm.RegisterTransaction(now, nt)
			lg.logf("[%d] REGISTER now=%d %s card=%s merch=%s amt=%d settle=%d -> sys=%v naive=%v",
				i, now, id, card, merch, amt, settle, e1, e2)
			cmpErr("register", e1, e2)
			if e1 == nil {
				txns = append(txns, info)
				merchants[merch] = true
			}
		case k == 1 && len(txns) > 0:
			m := []string{"mA", "mB"}[rng.Intn(2)]
			amt := int64(10 + rng.Intn(200))
			e1 := sys.CreditMerchant(now, m, amt)
			e2 := nm.CreditMerchant(now, m, amt)
			lg.logf("[%d] CREDIT now=%d merch=%s amt=%d -> %v/%v", i, now, m, amt, e1, e2)
			cmpErr("credit", e1, e2)
			merchants[m] = true
			if e1 == nil {
				creditLedger[now] += amt
			}
		case k >= 2 && k <= 5 && len(txns) > 0:
			info := txns[rng.Intn(len(txns))]
			reason := cb.Reason(rng.Intn(3))
			amt := int64(1 + rng.Intn(int(info.amount)+20))
			cid := fmt.Sprintf("C%04d", len(caseIDs))
			if rng.Intn(6) == 0 && len(caseIDs) > 0 {
				cid = caseIDs[rng.Intn(len(caseIDs))] // sometimes duplicate id
			}
			e1 := sys.File(now, info.id, reason, amt, cid)
			e2 := nm.File(now, info.id, naive.Reason(reason), amt, cid)
			lg.logf("[%d] FILE now=%d txn=%s reason=%d amt=%d case=%s -> sys=%v naive=%v",
				i, now, info.id, reason, amt, cid, e1, e2)
			cmpErr("file", e1, e2)
			if e1 == nil {
				caseIDs = append(caseIDs, cid)
			}
		case k >= 6 && k <= 8 && len(caseIDs) > 0:
			cid := caseIDs[rng.Intn(len(caseIDs))]
			kind := rng.Intn(3)
			var e1, e2 error
			name := ""
			switch kind {
			case 0:
				name, e1, e2 = "DEFEND", sys.Defend(now, cid), nm.Defend(now, cid)
			case 1:
				name, e1, e2 = "ACCEPT", sys.AcceptDefense(now, cid), nm.AcceptDefense(now, cid)
			default:
				name, e1, e2 = "PREARB", sys.PreArbitration(now, cid), nm.PreArbitration(now, cid)
			}
			lg.logf("[%d] %s now=%d case=%s -> sys=%v naive=%v", i, name, now, cid, e1, e2)
			cmpErr(name, e1, e2)
		case k == 9 && len(caseIDs) > 0:
			cid := caseIDs[rng.Intn(len(caseIDs))]
			winner := cb.Outcome(1 + rng.Intn(2))
			e1 := sys.Rule(now, cid, winner)
			e2 := nm.Rule(now, cid, naive.Outcome(winner))
			lg.logf("[%d] RULE now=%d case=%s winner=%d -> sys=%v naive=%v",
				i, now, cid, winner, e1, e2)
			cmpErr("rule", e1, e2)
			if e1 == nil {
				fee := cfg.c.ArbitrationFee
				if winner == cb.OutcomeMerchantWin {
					issuerFeeLedger[now] += fee
				} else {
					merchFeeLedger[now] += fee
				}
			}
		case k >= 10:
			// Invalid-argument probes to exercise precedence.
			if rng.Intn(2) == 0 {
				e1 := sys.File(now, "", cb.ReasonFraud, 1, "X")
				e2 := nm.File(now, "", naive.ReasonFraud, 1, "X")
				cmpErr("invalid file", e1, e2)
			}
		}

		// Query cross-check on (sometimes) a slightly later read time.
		qnow := now
		if rng.Intn(2) == 0 {
			qnow += rng.Intn(4)
		}
		for _, m := range []string{"mA", "mB"} {
			b1, e1 := sys.MerchantBalance(qnow, m)
			b2, e2 := nm.MerchantBalance(qnow, m)
			cmpErr("balance err", e1, e2)
			if b1 != b2 {
				failf("merchant %s balance at %d: sys=%d naive=%d", m, qnow, b1, b2)
			}
		}
		i1, _ := sys.IssuerAccount(qnow)
		i2, _ := nm.IssuerAccount(qnow)
		if i1 != i2 {
			failf("issuer at step %d qnow=%d now=%d: sys=%d naive=%d", i, qnow, now, i1, i2)
		}
		p1, _ := sys.PendingHeld(qnow)
		p2, _ := nm.PendingHeld(qnow)
		if p1 != p2 {
			failf("pending at %d: sys=%d naive=%d", qnow, p1, p2)
		}
		// Conservation (excluding fees): sum of all merchant balances +
		// issuer + pending == total merchant credits. Fees are the only flow
		// outside this identity, so compute fees as the negative issuer fees
		// booked on merchant-wins minus merchant fees; here both engines agree
		// on issuer/pending/merchant already, and we assert the identity on the
		// production engine using a naive-derived credit total.
		var sumMerch int64
		for _, m := range []string{"mA", "mB"} {
			b, _ := sys.MerchantBalance(qnow, m)
			sumMerch += b
		}
		// Fees: issuer account includes merchant-win fees as negative; total
		// identity is sumMerch + issuer + pending + totalFees == credits.
		// totalFees is tracked via issuer's fee component; derive credits from
		// the harness credit accumulator at qnow.
		if got := sumMerch + i1 + p1 + totalFeesBefore(qnow); got != creditsBefore(qnow) {
			failf("conservation at %d: merch=%d issuer=%d pending=%d fees=%d sum=%d credits=%d",
				qnow, sumMerch, i1, p1, totalFeesBefore(qnow), got, creditsBefore(qnow))
		}
		for _, id := range txnIDs() {
			d1, e1 := sys.Disputable(qnow, id)
			d2, e2 := nm.Disputable(qnow, id)
			cmpErr("disputable err", e1, e2)
			if d1 != d2 {
				failf("disputable %s at %d: sys=%d naive=%d", id, qnow, d1, d2)
			}
		}
		for _, cid := range caseIDs {
			st1, o1, e1 := sys.CaseStatus(qnow, cid)
			st2, o2, e2 := nm.CaseStatus(qnow, cid)
			cmpErr("status err", e1, e2)
			if st1 != cb.Status(st2) || o1 != cb.Outcome(o2) {
				failf("case %s at %d: sys=(%v,%v) naive=(%v,%v)",
					cid, qnow, st1, o1, st2, o2)
			}
		}
	}

	// Independent money-conservation check over a final sweep of read times:
	// merchant + issuer + pending == total credits (fees excluded from sum
	// since fees are the documented external flow). Here we verify it via the
	// production ledger directly for every queried day by re-deriving total
	// credits from the naive model is unnecessary; instead compare both
	// engines' triples pointwise which the loop above already guarantees.
	if *verboseDiff {
		lg.flush(t)
	}
}

func errName(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	for trial := 0; trial < 60; trial++ {
		t.Run(fmt.Sprintf("trial%d", trial), func(t *testing.T) {
			runDifferential(t, rand.New(rand.NewSource(rng.Int63())), 180)
		})
	}
}
