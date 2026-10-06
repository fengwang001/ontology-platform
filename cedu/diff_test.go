package cedu

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

type diffOp struct {
	kind              int // 0 credit 1 correct 2 revoke 3 status
	now               int
	cat               Category
	credits, earnedOn int
	org               string
	target            int
}

func genDiffOps(rng *rand.Rand, n int) []diffOp {
	orgs := []string{"orgA", "orgB", "orgC"}
	ops := make([]diffOp, 0, n)
	now := 0
	recCount := 0
	for i := 0; i < n; i++ {
		roll := rng.Intn(100)
		switch {
		case roll < 65:
			now += rng.Intn(3)
			earned := now
			if rng.Intn(4) == 0 && now > 0 {
				earned = rng.Intn(now + 1)
			}
			ops = append(ops, diffOp{
				kind: 0, now: now, cat: Category(rng.Intn(3)),
				credits: 1 + rng.Intn(25), earnedOn: earned,
				org: orgs[rng.Intn(len(orgs))],
			})
			recCount++
		case roll < 80 && recCount > 0:
			now += rng.Intn(2)
			kind := 1
			if rng.Intn(3) == 0 {
				kind = 2
			}
			ops = append(ops, diffOp{
				kind: kind, now: now, target: rng.Intn(recCount),
				org: orgs[rng.Intn(len(orgs))], credits: 1 + rng.Intn(30),
			})
		default:
			now += rng.Intn(4)
			ops = append(ops, diffOp{kind: 3, now: now})
		}
	}
	return ops
}

func codeOf(err error) ErrorCode {
	if err == nil {
		return 0
	}
	return err.(*Error).Code
}

func diffCfg() Config {
	return Config{
		CycleLength: 8, TotalRequired: 24, RequiredMin: 8,
		RequiredCap: 16, ElectiveCap: 16, GraceDays: 2,
		CorrectDays: 4, CarryoverCap: 6,
	}
}

type fatalHelper interface {
	Helper()
	Fatalf(string, ...any)
	Fatal(...any)
	Logf(string, ...any)
}

func runDiff(t fatalHelper, seed int64, verbose bool) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	ops := genDiffOps(rng, 80+rng.Intn(120))
	cfg := diffCfg()
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	nv := newNaive(cfg)
	const holder = "H"
	if err := svc.RegisterHolder(RegisterInput{HolderID: holder, IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	if err := nv.register(RegisterInput{HolderID: holder, IssueDate: 0, Now: 0}); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	var ids []string // 成功登记记录的朴素 ID（与在线 ID 同序同值）

	cmpStatus := func(step int) {
		t.Helper()
		hh := nv.h[holder]
		got, gerr := svc.Status(holder, hh.lastNow)
		if gerr != nil {
			t.Fatalf("step %d status: %v\n%s", step, gerr, log.String())
		}
		want := nv.snapshot(hh, hh.lastNow)
		if got.Expired != nv.evaluate(hh, hh.lastNow).expired {
			t.Fatalf("step %d expired mismatch got=%v\n%s", step, got.Expired, log.String())
		}
		if !got.Expired && got.CurrentCycle != want {
			t.Fatalf("step %d cycle mismatch\ngot =%+v\nwant=%+v\n%s",
				step, got.CurrentCycle, want, log.String())
		}
	}

	for i, o := range ops {
		var e1, e2 error
		switch o.kind {
		case 0:
			in := CreditInput{
				HolderID: holder, Category: o.cat, Credits: o.credits,
				EarnedOn: o.earnedOn, Org: o.org, Now: o.now,
			}
			var id string
			id, e1 = svc.RegisterCredit(in)
			_, e2 = nv.credit(in)
			if e2 == nil && e1 == nil {
				ids = append(ids, id)
			}
			fmt.Fprintf(&log, "step %d REG now=%d cat=%d cr=%d on=%d org=%s -> %s\n",
				i, o.now, o.cat, o.credits, o.earnedOn, o.org, codeOf(e1))
		case 1, 2:
			if o.target >= len(ids) {
				continue
			}
			rid := ids[o.target]
			if o.kind == 1 {
				e1 = svc.CorrectCredit(holder, rid, o.org, o.credits, o.now)
				e2 = nv.correctOrRevoke(holder, rid, o.org, o.credits, o.now, false)
			} else {
				e1 = svc.RevokeCredit(holder, rid, o.org, o.now)
				e2 = nv.correctOrRevoke(holder, rid, o.org, 1, o.now, true)
			}
			fmt.Fprintf(&log, "step %d %s rid=%s org=%s now=%d -> %s\n",
				i, map[bool]string{true: "REVOKE", false: "CORRECT"}[o.kind == 2],
				rid, o.org, o.now, codeOf(e1))
		case 3:
			_, e1 = svc.Status(holder, o.now)
			if hh := nv.h[holder]; hh != nil && o.now >= nv.lastNow {
				nv.lastNow = o.now
				hh.lastNow = o.now
			}
			fmt.Fprintf(&log, "step %d QUERY now=%d\n", i, o.now)
		}
		if codeOf(e1) != codeOf(e2) {
			t.Fatalf("step %d op=%+v\ngot=%v (%v)\nwant=%v (%v)\n%s",
				i, o, codeOf(e1), e1, codeOf(e2), e2, log.String())
		}
		cmpStatus(i)
	}
	if verbose && testing.Verbose() {
		t.Logf("seed=%d trace:\n%s", seed, log.String())
	}
}

func TestDifferentialRandom(t *testing.T) {
	for seed := int64(1); seed <= 300; seed++ {
		runDiff(t, seed*7919+1, seed == 1)
	}
}
