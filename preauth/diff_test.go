package preauth

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// diffLogger 记录每个随机操作的输入、两模型输出与判定依据。
// 失败时整段转储，便于复现与核对。
type diffLogger struct {
	b strings.Builder
}

func (l *diffLogger) logf(format string, args ...any) {
	fmt.Fprintf(&l.b, format+"\n", args...)
}

func (l *diffLogger) String() string { return l.b.String() }

type opKind int

const (
	opAuthorize opKind = iota
	opIncrement
	opCapture
	opVoid
	opRefund
	opAdjust
	opQuery
)

func TestRandomDifferential(t *testing.T) {
	const (
		seeds      = 2000
		maxOps     = 120
		accountsN  = 3
		baseCredit = 500
		E          = 4
		bps        = 1500 // 15%
	)

	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		lg := &diffLogger{}
		lg.logf("=== seed=%d E=%d bps=%d credit=%d ===", seed, E, bps, baseCredit)

		sys := NewSystem(Config{ValidityDays: E, ToleranceBPS: bps})
		nav := newNaive(E, bps)

		accs := make([]string, accountsN)
		for i := range accs {
			accs[i] = "acc" + strconv.Itoa(i)
			now := rng.Intn(3)
			e1 := sys.CreateAccount(accs[i], baseCredit, int64(now))
			e2 := nav.createAccount(accs[i], baseCredit, int64(now))
			if fmt.Sprint(e1) != fmt.Sprint(e2) {
				t.Fatalf("seed %d create mismatch %v vs %v\n%s", seed, e1, e2, lg)
			}
			lg.logf("create %s credit=%d now=%d -> %v", accs[i], baseCredit, now, e1)
		}

		var liveAuths []string
		allAuthsCount := 0
		clock := int64(0)

		for step := 0; step < maxOps; step++ {
			now := clock
			// 时间：大概率前进或停滞，偶尔小跳，单调不减。
			switch rng.Intn(10) {
			case 0, 1, 2, 3:
				now += int64(rng.Intn(3))
			case 4:
				now += int64(rng.Intn(8))
			}

			kind := opKind(rng.Intn(int(opQuery) + 1))
			accID := accs[rng.Intn(len(accs))]

			// 偶尔构造时钟回退（用上一次 clock 之前的值）。
			backward := rng.Intn(12) == 0
			opNow := now
			if backward && clock > 0 {
				opNow = rng.Int63n(clock)
			}

			switch kind {
			case opAuthorize:
				id := fmt.Sprintf("a%d", allAuthsCount)
				allAuthsCount++
				// 少量重复复用历史编号。
				if len(liveAuths) > 0 && rng.Intn(6) == 0 {
					id = liveAuths[rng.Intn(len(liveAuths))]
				}
				amt := int64(1 + rng.Intn(300))
				got := sys.Authorize(id, accID, amt, opNow)
				want := nav.authorize(id, accID, amt, opNow)
				lg.logf("authorize id=%s acc=%s amt=%d now=%d -> got=%v want=%v", id, accID, amt, opNow, got, want)
				cmpErr(t, lg, seed, got, want)
				if got == nil {
					liveAuths = append(liveAuths, id)
				}
			case opIncrement:
				if len(liveAuths) == 0 {
					continue
				}
				id := liveAuths[rng.Intn(len(liveAuths))]
				amt := int64(1 + rng.Intn(200))
				got := sys.Increment(id, amt, opNow)
				want := nav.increment(id, amt, opNow)
				lg.logf("increment id=%s amt=%d now=%d -> got=%v want=%v", id, amt, opNow, got, want)
				cmpErr(t, lg, seed, got, want)
			case opCapture:
				if len(liveAuths) == 0 {
					continue
				}
				id := liveAuths[rng.Intn(len(liveAuths))]
				amt := int64(1 + rng.Intn(260))
				final := rng.Intn(5) == 0
				got := sys.Capture(id, amt, final, opNow)
				want := nav.capture(id, amt, final, opNow)
				lg.logf("capture id=%s amt=%d final=%v now=%d -> got=%v want=%v", id, amt, final, opNow, got, want)
				cmpErr(t, lg, seed, got, want)
			case opVoid:
				if len(liveAuths) == 0 {
					continue
				}
				id := liveAuths[rng.Intn(len(liveAuths))]
				got := sys.Void(id, opNow)
				want := nav.void(id, opNow)
				lg.logf("void id=%s now=%d -> got=%v want=%v", id, opNow, got, want)
				cmpErr(t, lg, seed, got, want)
			case opRefund:
				if len(liveAuths) == 0 {
					continue
				}
				id := liveAuths[rng.Intn(len(liveAuths))]
				amt := int64(1 + rng.Intn(120))
				got := sys.Refund(id, amt, opNow)
				want := nav.refund(id, amt, opNow)
				lg.logf("refund id=%s amt=%d now=%d -> got=%v want=%v", id, amt, opNow, got, want)
				cmpErr(t, lg, seed, got, want)
			case opAdjust:
				credit := int64(1 + rng.Intn(900))
				got := sys.AdjustCredit(accID, credit, opNow)
				want := nav.adjust(accID, credit, opNow)
				lg.logf("adjust acc=%s credit=%d now=%d -> got=%v want=%v", accID, credit, opNow, got, want)
				cmpErr(t, lg, seed, got, want)
			case opQuery:
				// 用当前（非回退）时刻查询，逐账户逐授权对照。
				for _, aid := range accs {
					gAv, gErr := sys.Available(aid, now)
					nAcc := nav.accounts[aid]
					wAv, wErr := nav.queryAvailable(aid, now)
					lg.logf("query avail acc=%s now=%d -> got=(%d,%v) want=(%d,%v)", aid, now, gAv, gErr, wAv, wErr)
					if fmt.Sprint(gErr) != fmt.Sprint(wErr) || gAv != wAv {
						t.Fatalf("seed %d avail mismatch acc=%s now=%d got=(%d,%v) want=(%d,%v)\n%s",
							seed, aid, now, gAv, gErr, wAv, wErr, lg)
					}
					for id := range nAcc.auths {
						gv, gerr := sys.Auth(id, now)
						wv, werr := nav.queryAuth(id, now)
						if fmt.Sprint(gerr) != fmt.Sprint(werr) {
							t.Fatalf("seed %d auth %s err mismatch got=%v want=%v\n%s", seed, id, gerr, werr, lg)
						}
						if gerr != nil {
							continue
						}
						cmpView(t, lg, seed, id, now, gv, wv)
					}
					if gAv < 0 {
						t.Fatalf("seed %d negative available %d\n%s", seed, gAv, lg)
					}
				}
			}

			// 时钟只在操作被接受（非回退）时前进；与两模型内部 lastNow 一致。
			if !backward && opNow > clock {
				clock = opNow
			}
		}
	}
}

func cmpErr(t *testing.T, lg *diffLogger, seed int64, got, want error) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("seed %d error mismatch got=%v want=%v\n%s", seed, got, want, lg)
	}
}

func cmpView(t *testing.T, lg *diffLogger, seed int64, id string, now int64, got, want AuthView) {
	t.Helper()
	if got != want {
		t.Fatalf("seed %d auth %s mismatch now=%d got=%+v want=%+v\n%s", seed, id, now, got, want, lg)
	}
}
