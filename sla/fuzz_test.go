package sla

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// codeName 将错误码转成稳定的判定依据描述。
func codeName(c Code) string {
	switch c {
	case CodeOK:
		return "ok"
	case CodeInvalidParam:
		return "invalid-param"
	case CodeClockRollback:
		return "clock-rollback"
	case CodeOrderNotFound:
		return "order-not-found"
	case CodeOrderCanceled:
		return "order-canceled"
	case CodeEventOrder:
		return "event-order"
	case CodeAlreadyReaddressed:
		return "already-readdressed"
	case CodeAlreadyPaid:
		return "already-paid"
	case CodeNotDelivered:
		return "not-delivered"
	case CodeWindowClosed:
		return "window-closed"
	case CodeNoDelay:
		return "no-delay"
	default:
		return "unknown"
	}
}

func opString(op nOp) string {
	switch op.kind {
	case "weather":
		return fmt.Sprintf("AddWeather(%s,[%d,%d),ext=%d)", op.wid, op.wl, op.wr, op.wext)
	default:
		return fmt.Sprintf("%s(%s,t=%d)", op.kind, op.id, op.at)
	}
}

// runDifferential 用同一随机操作序列驱动生产系统与朴素模型，
// 逐步比较错误码与裁决；log=true 时打印每步输入/输出/判定依据。
func runDifferential(t *testing.T, rng *rand.Rand, nsteps, norders int, log bool) {
	t.Helper()
	cfg := Config{
		PromiseDuration:  60,
		MerchantPrep:     int64(rng.Intn(25)),
		PlatformDispatch: int64(rng.Intn(15)),
		RiderPickup:      int64(rng.Intn(15)),
		UserAddrExtend:   int64(5 + rng.Intn(20)),
		ExtendCap:        int64(rng.Intn(80)),
		ClaimWindow:      int64(20 + rng.Intn(60)),
		TierThresholds:   []int64{10, 30, 60},
		TierPayouts:      []int64{5, 15, 40},
	}
	sys := newSystem(cfg)
	nav := newNaive()
	nav.cfgs[""] = cfg

	var b strings.Builder
	fmt.Fprintf(&b, "cfg=%+v\n", cfg)

	clock := int64(0)
	for step := 0; step < nsteps; step++ {
		id := fmt.Sprintf("o%d", rng.Intn(norders))
		tick := int64(rng.Intn(12))
		clock += tick
		at := clock + int64(rng.Intn(3))

		var op nOp
		var sysCode Code
		var sysRuling *Ruling
		var sysErr error

		switch k := rng.Intn(10); k {
		case 0:
			op = nOp{kind: "accept", id: id, at: at}
			sysErr = sys.Accept(id, at)
		case 1:
			op = nOp{kind: "dispatch", id: id, at: at}
			sysErr = sys.Dispatch(id, at)
		case 2:
			op = nOp{kind: "ready", id: id, at: at}
			sysErr = sys.Ready(id, at)
		case 3:
			op = nOp{kind: "pickup", id: id, at: at}
			sysErr = sys.Pickup(id, at)
		case 4:
			op = nOp{kind: "deliver", id: id, at: at}
			sysErr = sys.Deliver(id, at)
		case 5:
			op = nOp{kind: "readdress", id: id, at: at}
			sysErr = sys.ChangeAddress(id, at)
		case 6:
			wid := fmt.Sprintf("w%d", rng.Intn(nsteps/8+2))
			l := int64(rng.Intn(200))
			r := l + int64(1+rng.Intn(120))
			ext := int64(rng.Intn(40))
			op = nOp{kind: "weather", wid: wid, wl: l, wr: r, wext: ext}
			sysErr = sys.AddWeather(wid, l, r, ext)
		case 7:
			op = nOp{kind: "cancel", id: id}
			sysErr = sys.Cancel(id)
		case 8:
			op = nOp{kind: "claim", id: id, at: at}
			sysRuling, sysErr = sys.Claim(id, at)
		default:
			op = nOp{kind: "auto", id: id, at: at}
			sysRuling, sysErr = sys.AutoAdjudicate(id, at)
		}
		if sysErr != nil {
			sysCode = sysErr.(*Error).Code
		}

		nCode, nEntry := nav.step(op)

		reason := codeName(sysCode)
		fmt.Fprintf(&b, "step %3d %-40s -> %s", step, opString(op), reason)
		if sysRuling != nil {
			fmt.Fprintf(&b, " party=%s delay=%d payout=%d", sysRuling.Party, sysRuling.Delay, sysRuling.Payout)
		}
		fmt.Fprintln(&b)

		if sysCode != nCode {
			if log {
				t.Logf("\n%s", b.String())
			}
			t.Fatalf("step %d %s: code mismatch sys=%s naive=%s\n%s",
				step, opString(op), codeName(sysCode), codeName(nCode), b.String())
		}
		if sysRuling != nil && nEntry != nil {
			if sysRuling.Party != nEntry.Party ||
				sysRuling.Delay != nEntry.Delay ||
				sysRuling.Payout != nEntry.Amount {
				if log {
					t.Logf("\n%s", b.String())
				}
				t.Fatalf("step %d %s: ruling mismatch sys={%s d=%d p=%d} naive={%s d=%d p=%d}",
					step, opString(op),
					sysRuling.Party, sysRuling.Delay, sysRuling.Payout,
					nEntry.Party, nEntry.Delay, nEntry.Amount)
			}
		}
	}

	// 最终账目对比：顺序无关地按订单聚合比较。
	sysLedger := sys.Ledger()
	navCodes := map[string]LedgerEntry{}
	_, nFinal := nav.replay(nav.ops)
	_ = nFinal
	// 朴素模型账目：再做一次“扫描日志收集裁决”更直接——重放时 ledger 未导出，
	// 改为重放全部 claim/auto 操作定位。
	for _, op := range nav.ops {
		if op.kind != "claim" && op.kind != "auto" {
			continue
		}
		c, e := nav.replay(prefixThrough(nav.ops, op))
		if c == CodeOK && e != nil {
			if _, seen := navCodes[e.OrderID]; !seen {
				navCodes[e.OrderID] = *e
			}
		}
	}
	if len(sysLedger) != len(navCodes) {
		if log {
			t.Logf("\n%s", b.String())
		}
		t.Fatalf("ledger size mismatch sys=%d naive=%d", len(sysLedger), len(navCodes))
	}
	for _, e := range sysLedger {
		ne, ok := navCodes[e.OrderID]
		if !ok {
			t.Fatalf("order %s in sys ledger but not naive", e.OrderID)
		}
		if e.Party != ne.Party || e.Amount != ne.Amount || e.Delay != ne.Delay ||
			e.Automatic != ne.Automatic {
			t.Fatalf("ledger mismatch for %s sys=%+v naive=%+v", e.OrderID, e, ne)
		}
	}

	if log {
		keys := make([]string, 0, len(sysLedger))
		for _, e := range sysLedger {
			keys = append(keys, e.OrderID)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "final ledger (%d):\n", len(sysLedger))
		for _, k := range keys {
			e := navCodes[k]
			fmt.Fprintf(&b, "  %s party=%s delay=%d payout=%d auto=%v\n",
				k, e.Party, e.Delay, e.Amount, e.Automatic)
		}
		t.Logf("\n%s", b.String())
	}
}

// prefixThrough 返回到某个具体操作（按出现次序匹配 kind+id+at）为止的日志前缀。
func prefixThrough(ops []nOp, target nOp) []nOp {
	count := 0
	for i, op := range ops {
		if op == target {
			count = i + 1
		}
	}
	return ops[:count]
}

func TestDifferentialSmall(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	runDifferential(t, rng, 400, 4, testing.Verbose())
}

func TestDifferentialManySeeds(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		runDifferential(t, rng, 300, 6, false)
	}
}
