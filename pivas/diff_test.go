package pivas

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestNaiveDifferential 以 1500 组随机操作序列对照主系统与独立朴素模型。
// 每一步记录输入、双方输出与判定依据；任何分歧立即失败并打印完整轨迹。
func TestNaiveDifferential(t *testing.T) {
	const sequences = 1500
	const maxOps = 40
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		sys := New()
		nav := newNaive()
		env := newDiffEnv(t, rng, sys, nav)
		var log []string
		for op := 0; op < maxOps; op++ {
			entry, sysErr, navErr := env.step(int(seed), op)
			log = append(log, entry)
			if codeOf(sysErr) != codeOf(navErr) {
				for _, l := range log {
					t.Log(l)
				}
				t.Fatalf("seed=%d op=%d error mismatch sys=%v naive=%v\n%s",
					seed, op, sysErr, navErr, entry)
			}
			if sysErr == nil {
				env.compareSnapshots(t, int(seed), op, log)
			}
		}
		if testing.Verbose() && seed < 3 {
			for _, l := range log {
				t.Log(l)
			}
		}
	}
}

func codeOf(err error) ErrorCode {
	if err == nil {
		return 0
	}
	return err.(*Error).Code
}

type diffEnv struct {
	t        *testing.T
	rng      *rand.Rand
	sys      *System
	nav      *naive
	drugs    []string
	solvents map[string]bool
	orders   []string
	now      int
}

func newDiffEnv(t *testing.T, rng *rand.Rand, sys *System, nav *naive) *diffEnv {
	e := &diffEnv{t: t, rng: rng, sys: sys, nav: nav, solvents: map[string]bool{}}
	nb := 1 + rng.Intn(2)
	for i := 0; i < nb; i++ {
		b := Bench{ID: fmt.Sprintf("B%d", i), Capacity: 1 + rng.Intn(3), ClearGap: 1 + rng.Intn(20)}
		must(t, sys.RegisterBench(0, b))
		must(t, nav.addBench(0, b))
	}
	base := 5 + rng.Intn(20)
	for n := 1; n <= 3; n++ {
		base += 5 + rng.Intn(30)
		must(t, sys.SetDuration(0, n, base))
		must(t, nav.setDuration(0, n, base))
	}
	roomT := 1 + rng.Intn(30)
	coldT := 1 + rng.Intn(30)
	must(t, sys.SetTransport(0, Room, roomT))
	must(t, nav.setTransport(0, Room, roomT))
	must(t, sys.SetTransport(0, Cold, coldT))
	must(t, nav.setTransport(0, Cold, coldT))

	nd := 2 + rng.Intn(4)
	for i := 0; i < nd; i++ {
		id := fmt.Sprintf("d%d", i)
		sv := fmt.Sprintf("S%d", rng.Intn(2))
		e.solvents[sv] = true
		d := Drug{
			RoomStableSec:  20 + rng.Intn(400),
			ColdStableSec:  20 + rng.Intn(800),
			LightSensitive: rng.Intn(3) == 0,
			SolventClass:   sv,
		}
		must(t, sys.RegisterDrug(0, id, d))
		must(t, nav.addDrug(0, id, d))
		e.drugs = append(e.drugs, id)
	}
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func (e *diffEnv) advance() int {
	if e.rng.Intn(3) != 0 {
		e.now += e.rng.Intn(40)
	}
	return e.now
}

func (e *diffEnv) step(seed, op int) (string, error, error) {
	r := e.rng.Float64()
	now := e.advance()
	switch {
	case r < 0.62:
		return e.accept(now)
	case r < 0.80 && len(e.orders) > 0:
		id := e.orders[e.rng.Intn(len(e.orders))]
		sysErr := e.sys.CancelOrder(now, id)
		navErr := e.nav.cancel(now, id)
		if sysErr == nil {
			e.orders = removeStr(e.orders, id)
		}
		return fmt.Sprintf("seed=%d op=%d CANCEL now=%d id=%s -> sys=%v naive=%v",
			seed, op, now, id, sysErr, navErr), sysErr, navErr
	case r < 0.90:
		a := e.drugs[e.rng.Intn(len(e.drugs))]
		b := e.drugs[e.rng.Intn(len(e.drugs))]
		for b == a {
			b = e.drugs[e.rng.Intn(len(e.drugs))]
		}
		sysErr := e.sys.AddIncompatibility(now, a, b)
		navErr := e.nav.addBad(now, a, b)
		return fmt.Sprintf("seed=%d op=%d ADDBAD now=%d %s-%s -> sys=%v naive=%v",
			seed, op, now, a, b, sysErr, navErr), sysErr, navErr
	default:
		id := e.drugs[e.rng.Intn(len(e.drugs))]
		sv := pickKey(e.rng, e.solvents)
		d := Drug{
			RoomStableSec:  20 + e.rng.Intn(400),
			ColdStableSec:  20 + e.rng.Intn(800),
			LightSensitive: e.rng.Intn(3) == 0,
			SolventClass:   sv,
		}
		sysErr := e.sys.RegisterDrug(now, id, d)
		navErr := e.nav.addDrug(now, id, d)
		return fmt.Sprintf("seed=%d op=%d CHGDRUG now=%d %s -> sys=%v naive=%v",
			seed, op, now, id, sysErr, navErr), sysErr, navErr
	}
}

func (e *diffEnv) accept(now int) (string, error, error) {
	perm := e.rng.Perm(len(e.drugs))
	k := 1 + e.rng.Intn(3)
	if k > len(e.drugs) {
		k = len(e.drugs)
	}
	var drugs []string
	for _, idx := range perm[:k] {
		drugs = append(drugs, e.drugs[idx])
	}
	sv := e.nav.drugs[drugs[0]].SolventClass
	in := OrderInput{
		ID:      fmt.Sprintf("o%d", len(e.orders)+e.rng.Intn(100000)),
		Drugs:   drugs,
		Solvent: sv,
		DueAt:   now + e.rng.Intn(900),
		Urgent:  e.rng.Intn(2) == 0,
	}
	sysErr := e.sys.AcceptOrder(now, in)
	navErr := e.nav.accept(now, in)
	if sysErr == nil {
		e.orders = append(e.orders, in.ID)
	}
	reason := "accepted"
	if sysErr != nil {
		reason = codeName(codeOf(sysErr))
	}
	return fmt.Sprintf("ACCEPT now=%d id=%s drugs=%v solvent=%s due=%d urgent=%v -> %s",
		now, in.ID, in.Drugs, in.Solvent, in.DueAt, in.Urgent, reason), sysErr, navErr
}

func (e *diffEnv) compareSnapshots(t *testing.T, seed, op int, log []string) {
	t.Helper()
	navSnap := e.nav.snapshot()
	for id, nv := range navSnap {
		info, err := e.sys.QueryOrder(e.now, id)
		if err != nil {
			for _, l := range log {
				t.Log(l)
			}
			t.Fatalf("seed=%d op=%d sys missing %s: %v", seed, op, id, err)
		}
		if info.BenchID != nv.bench ||
			info.BatchStart != nv.start ||
			info.ReadyAt != nv.ready ||
			info.Storage != nv.st ||
			info.DeliverAt != nv.deliver ||
			info.ExpireAt != nv.expire ||
			info.OnTime != nv.onTime ||
			info.Covered != nv.cover {
			for _, l := range log {
				t.Log(l)
			}
			t.Fatalf("seed=%d op=%d order=%s mismatch\n sys=%+v\n naive=%+v",
				seed, op, id, info, nv)
		}
	}
	if len(navSnap) != len(e.sys.orders) {
		t.Fatalf("seed=%d op=%d order count sys=%d naive=%d",
			seed, op, len(e.sys.orders), len(navSnap))
	}
}

func removeStr(xs []string, x string) []string {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

func pickKey(rng *rand.Rand, m map[string]bool) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys[rng.Intn(len(keys))]
}
