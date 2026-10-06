package deposit

import (
	"math/rand"
	"strings"
	"testing"
)

// op 是差分测试中的一步操作。
type op struct {
	kind string
	now  int
	id   int
	cat  Category
	amt  int64
}

// 随机操作序列驱动真实服务与独立朴素模型，逐步对照错误码、退还本金/违约金
// 与全部账目；日志打印每步输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	const iterations = 400
	for seed := int64(1); seed <= 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := Config{
			A:       rng.Intn(7),
			B:       rng.Intn(5),
			C:       rng.Intn(6),
			RateNum: 1,
			RateDen: int64(2 + rng.Intn(99)),
		}
		var log strings.Builder
		logger := func(line string) { log.WriteString(line + "\n") }
		svc := NewService(cfg, logger)
		nm := newNaive(cfg)

		deposit := int64(1 + rng.Intn(500))
		startNow := rng.Intn(5)

		runStep := func(o op) nResult {
			var r nResult
			switch o.kind {
			case "checkout":
				r = nm.doCheckout(o.now, o.amt)
			case "file":
				r = nm.file(o.now, o.cat, o.amt)
			case "withdraw":
				r = nm.withdraw(o.now, o.id)
			case "dispute":
				r = nm.dispute(o.now, o.id)
			case "adjudge":
				r = nm.adjudge(o.now, o.id, o.amt)
			case "refund":
				r = nm.refund(o.now)
			}

			var gotCode ErrCode
			var gotP, gotL int64
			switch o.kind {
			case "checkout":
				gotCode = codeOf(svc.Checkout(o.now, "L", o.amt))
			case "file":
				gotID, err := svc.FileClaim(o.now, "L", o.cat, o.amt)
				gotCode = codeOf(err)
				if err == nil {
					if gotID != r.id {
						t.Fatalf("seed file id real=%d naive=%d", gotID, r.id)
					}
				}
			case "withdraw":
				gotCode = codeOf(svc.WithdrawClaim(o.now, "L", o.id))
			case "dispute":
				gotCode = codeOf(svc.Dispute(o.now, "L", o.id))
			case "adjudge":
				gotCode = codeOf(svc.Adjudicate(o.now, "L", o.id, o.amt))
			case "refund":
				var err error
				gotP, gotL, err = svc.Refund(o.now, "L")
				gotCode = codeOf(err)
			}

			if gotCode != r.code {
				t.Fatalf("seed=%d step %s %+v: code real=%v naive=%v\nLOG:\n%s",
					seed, o.kind, o, gotCode, r.code, log.String())
			}
			if o.kind == "refund" && gotCode == ErrOK && (gotP != r.principal || gotL != r.liability) {
				t.Fatalf("seed=%d refund %+v: real=(%d,%d) naive=(%d,%d)\nLOG:\n%s",
					seed, o, gotP, gotL, r.principal, r.liability, log.String())
			}

			if nm.ready && o.now >= nm.lastNow {
				nr, nv, nf, np, nrec, nref, nli := nm.accounts(o.now)
				snap, verr := svc.View(o.now, "L")
				if verr != nil {
					t.Fatalf("seed=%d view: %v", seed, verr)
				}
				if snap.Refunded != nr || snap.Vested != nv || snap.Frozen != nf ||
					snap.Pending != np || snap.Receivable != nrec ||
					snap.Refundable != nref || snap.Liability != nli {
					t.Fatalf("seed=%d step %s %+v accounts mismatch:\nreal r=%d v=%d f=%d p=%d rec=%d ref=%d li=%d\nnaiv r=%d v=%d f=%d p=%d rec=%d ref=%d li=%d\nLOG:\n%s",
						seed, o.kind, o,
						snap.Refunded, snap.Vested, snap.Frozen, snap.Pending, snap.Receivable, snap.Refundable, snap.Liability,
						nr, nv, nf, np, nrec, nref, nli, log.String())
				}
				if sum := snap.Refunded + snap.Vested + snap.Frozen + snap.Pending; sum != snap.Deposit {
					t.Fatalf("seed=%d conservation: %d != %d", seed, sum, snap.Deposit)
				}
			}
			return r
		}

		runStep(op{kind: "checkout", now: startNow, amt: deposit})

		maxDay := startNow + cfg.A + cfg.B + cfg.C + 8 + rng.Intn(10)
		var known []int
		now := startNow
		for i := 0; i < iterations; i++ {
			now += rng.Intn(3) // 时钟单调不降，含原地踏步
			if now > maxDay {
				now = maxDay
			}
			kind := []string{"file", "file", "withdraw", "dispute", "adjudge", "refund"}[rng.Intn(6)]
			// 偶尔注入时钟回退
			rollback := rng.Intn(15) == 0
			stepNow := now
			if rollback {
				stepNow = startNow - 1 - rng.Intn(3)
			}
			o := op{kind: kind, now: stepNow, cat: Category(rng.Intn(4)),
				amt: int64(1 + rng.Intn(int(deposit)+200))}
			// 偶尔构造非法类别/金额
			if rng.Intn(12) == 0 {
				o.cat = Category(7)
			}
			if rng.Intn(20) == 0 {
				o.amt = 0
			}
			if len(known) > 0 {
				o.id = known[rng.Intn(len(known))]
			} else {
				o.id = rng.Intn(3)
			}
			if kind == "adjudge" {
				o.amt = int64(rng.Intn(int(deposit) + 200))
			}
			r := runStep(o)
			if kind == "file" {
				if r.code == ErrOK && !containsID(known, r.id) {
					known = append(known, r.id)
				}
			}
		}
	}
}

func containsID(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
