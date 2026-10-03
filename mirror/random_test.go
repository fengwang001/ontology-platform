package mirror_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/diff"
	"ontology/mirror"
)

func randCfg(rng *rand.Rand) mirror.Config {
	return mirror.Config{
		K:           int64(rng.Intn(4) + 1),
		Cm:          rng.Intn(3) + 1,
		Bm:          int64(rng.Intn(6)),
		E:           rng.Intn(3) + 1,
		P:           int64(rng.Intn(50) + 1),
		AllowUnsafe: rng.Intn(2) == 0,
		Sensitive:   setOf("authorization", "cookie"),
		Ignore:      setOf("date"),
	}
}

func randFields(rng *rand.Rand) map[string]string {
	names := []string{"a", "b", "date", "x"}
	n := rng.Intn(3)
	f := map[string]string{}
	for i := 0; i < n; i++ {
		f[names[rng.Intn(len(names))]] = fmt.Sprint(rng.Intn(3))
	}
	return f
}

func randHeaders(rng *rand.Rand) map[string]string {
	hs := map[string]string{}
	pool := []struct{ l, u string }{
		{"authorization", "Authorization"},
		{"cookie", "COOKIE"},
		{"x-trace", "X-Trace"},
		{"x-shadow", "X-Shadow"},
		{"keep", "Keep"},
	}
	for _, h := range pool {
		if rng.Intn(2) == 0 {
			if rng.Intn(2) == 0 {
				hs[h.l] = "v"
			} else {
				hs[h.u] = "v"
			}
		}
	}
	return hs
}

func genOps(rng *rand.Rand, n int) []op {
	ops := make([]op, 0, n)
	now := int64(0)
	type dis struct {
		reqID string
		id    int64
	}
	var dispatched []dis
	var c int64
	s := 0
	var pausedUntil int64
	inFlight := 0
	nextID := 0
	doneFlag := map[int64]bool{}
	primaryFlag := map[string]bool{}
	methods := []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}
	cfg := currentCfg
	for len(ops) < n {
		now += int64(rng.Intn(3))
		kind := rng.Intn(10)
		switch {
		case kind < 5:
			var id string
			if rng.Intn(6) == 0 && len(dispatched) > 0 {
				id = dispatched[rng.Intn(len(dispatched))].reqID // 故意重复
			} else {
				id = fmt.Sprintf("r%03d", len(ops))
			}
			method := methods[rng.Intn(len(methods))]
			body := int64(rng.Intn(8))
			var hs map[string]string
			if rng.Intn(3) > 0 {
				hs = randHeaders(rng)
			}
			o := op{kind: opMirror, reqID: id, method: method, bodyLen: body, headers: hs, now: now}
			dup := false
			for _, d := range dispatched {
				if d.reqID == id {
					dup = true
				}
			}
			// 内联预测（仅用于生成有效引用；判定正误以朴素模拟为准）。
			predictDispatch := false
			if !dup {
				safe := method == "GET" || method == "HEAD"
				if (safe || cfg.AllowUnsafe) && body <= cfg.Bm && now >= pausedUntil {
					c++
					if c%cfg.K == 0 {
						if inFlight < cfg.Cm {
							predictDispatch = true
						}
					}
				}
			}
			ops = append(ops, o)
			if predictDispatch {
				nextID++
				inFlight++
				dispatched = append(dispatched, dis{reqID: id, id: int64(nextID)})
			}
		case kind < 7 && len(dispatched) > 0:
			pick := dispatched[rng.Intn(len(dispatched))]
			if primaryFlag[pick.reqID] {
				break // 允许产生重复 Primary（直接生成）
			}
			primaryFlag[pick.reqID] = true
			ops = append(ops, op{kind: opPrimary, reqID: pick.reqID,
				status: []int{200, 200, 200, 404, 500}[rng.Intn(5)], fields: randFields(rng)})
		case kind < 10 && len(dispatched) > 0:
			var id int64
			if rng.Intn(7) == 0 {
				id = int64(nextID + 5) // 故意未知
			} else {
				pick := dispatched[rng.Intn(len(dispatched))]
				id = pick.id
			}
			isErr := rng.Intn(3) == 0
			o := op{kind: opDoneErr, mirrorID: id, now: now}
			if !isErr {
				o.kind = opDoneOK
				o.status = []int{200, 200, 404, 500}[rng.Intn(4)]
				o.fields = randFields(rng)
			}
			if !doneFlag[id] && id <= int64(nextID) {
				doneFlag[id] = true
				inFlight--
				if isErr {
					if now >= pausedUntil {
						s++
						if s >= cfg.E {
							pausedUntil = now + cfg.P
							s = 0
						}
					}
				} else if now >= pausedUntil {
					s = 0
				}
			}
			ops = append(ops, o)
		}
	}
	return ops
}

var currentCfg mirror.Config

type realOutcome struct {
	result string
	id     int64
}

func runReal(t *testing.T, cfg mirror.Config, ops []op) ([]realOutcome, mirror.Stats) {
	t.Helper()
	m, err := mirror.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out := make([]realOutcome, len(ops))
	dispatched := map[int64]bool{}
	for i, o := range ops {
		switch o.kind {
		case opMirror:
			d, sk, e := m.Mirror(o.reqID, o.method, o.bodyLen, o.headers, o.now)
			if e != nil {
				out[i] = realOutcome{result: realErrName(e)}
			} else if sk != nil {
				out[i] = realOutcome{result: fmt.Sprintf("skip%d", sk.Reason)}
			} else {
				out[i] = realOutcome{result: "dispatch", id: d.MirrorID}
				dispatched[d.MirrorID] = true
			}
		case opPrimary:
			e := m.Primary(o.reqID, diff.Response{Status: o.status, Fields: o.fields})
			out[i] = realOutcome{result: realErrName(e)}
		case opDoneOK:
			e := m.Done(o.mirrorID, &diff.Response{Status: o.status, Fields: o.fields}, nil, o.now)
			out[i] = realOutcome{result: realErrName(e)}
		case opDoneErr:
			e := m.Done(o.mirrorID, nil, errBoom, o.now)
			out[i] = realOutcome{result: realErrName(e)}
		}
	}
	return out, m.Stats()
}

var errBoom = fmt.Errorf("boom")

// TestConcurrentSmoke 在 -race 下并发混合调用全部方法，验证可安全并发且在途不越限。
func TestConcurrentSmoke(t *testing.T) {
	cfg := mirror.Config{K: 1, Cm: 8, Bm: 100, E: 3, P: 5, AllowUnsafe: true,
		Sensitive: setOf("authorization"), Ignore: setOf("date")}
	m, _ := mirror.New(cfg)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			clock := int64(0)
			var ids []int64
			for i := 0; i < 200; i++ {
				clock++
				id := fmt.Sprintf("g%d-%d", g, i)
				d, _, _ := m.Mirror(id, "GET", 0, nil, clock)
				if d != nil {
					ids = append(ids, d.MirrorID)
					m.Primary(id, diff.Response{Status: 200, Fields: map[string]string{"a": "1"}})
					if rng.Intn(4) == 0 {
						m.Done(d.MirrorID, nil, errBoom, clock)
					} else {
						m.Done(d.MirrorID, &diff.Response{Status: 200, Fields: map[string]string{"a": "1", "b": "2"}}, nil, clock)
					}
				}
				m.Stats()
			}
			_ = ids
		}(g)
	}
	wg.Wait()
	st := m.Stats()
	if st.InFlight > cfg.Cm {
		t.Fatalf("并发下在途 %d 超过 Cm=%d", st.InFlight, cfg.Cm)
	}
	if st.Dispatched != int64(st.InFlight)+st.Completed {
		t.Fatalf("分派恒等式在并发终态不成立: %+v", st)
	}
}

func runNaive(cfg mirror.Config, ops []op) ([]realOutcome, mirror.Stats, *naive) {
	n := newNaive(cfg)
	out := make([]realOutcome, len(ops))
	for i, o := range ops {
		switch o.kind {
		case opMirror:
			res, id := n.mirrorOp(o)
			out[i] = realOutcome{result: res, id: id}
		case opPrimary:
			out[i] = realOutcome{result: n.primaryOp(o)}
		case opDoneOK:
			out[i] = realOutcome{result: n.doneOp(o, false)}
		case opDoneErr:
			out[i] = realOutcome{result: n.doneOp(o, true)}
		}
	}
	st := mirror.Stats{
		Accepted: n.accepted, SkippedUnsafe: n.skUnsafe, SkippedBody: n.skBody,
		SkippedPause: n.skPause, SkippedSample: n.skSample, SkippedBusy: n.skBusy,
		Dispatched: n.dispatched, InFlight: n.inFlight, Completed: n.completed,
		ShadowErrors: n.shadowErrors, Identical: n.identical, Compatible: n.compatible,
		Breaking: n.breaking, PendingPrimary: n.pendingPrimary, PendingDone: n.pendingDone,
		PausedUntil: n.pausedUntil,
	}
	return out, st, n
}

func checkInvariants(t *testing.T, st mirror.Stats, c int64, k int64, cm int) {
	t.Helper()
	skips := st.SkippedUnsafe + st.SkippedBody + st.SkippedPause + st.SkippedSample + st.SkippedBusy
	if st.Accepted != skips+st.Dispatched {
		t.Errorf("恒等式1: Accepted=%d != skips=%d + dispatched=%d", st.Accepted, skips, st.Dispatched)
	}
	if st.SkippedSample != c-c/k {
		t.Errorf("恒等式2: notSelected=%d != c-floor(c/k)=%d (c=%d k=%d)", st.SkippedSample, c-c/k, c, k)
	}
	if st.Dispatched != int64(st.InFlight)+st.Completed {
		t.Errorf("恒等式3: dispatched=%d != inFlight=%d + completed=%d", st.Dispatched, st.InFlight, st.Completed)
	}
	if st.Completed-st.ShadowErrors != st.Identical+st.Compatible+st.Breaking+st.PendingPrimary {
		t.Errorf("恒等式4: 成功完成=%d != 分类=%d + pendingPrimary=%d",
			st.Completed-st.ShadowErrors, st.Identical+st.Compatible+st.Breaking, st.PendingPrimary)
	}
	if st.InFlight > cm {
		t.Errorf("恒等式5: inFlight=%d > Cm=%d", st.InFlight, cm)
	}
}

// TestRandomAgainstNaive：2000 组随机序列与朴素逐步模拟逐项对照，并校验全部统计恒等式与重放确定性。
func TestRandomAgainstNaive(t *testing.T) {
	const groups = 2000
	const seqLen = 60
	for seed := int64(0); seed < groups; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := randCfg(rng)
		currentCfg = cfg
		ops := genOps(rng, seqLen)

		realOut, realSt := runReal(t, cfg, ops)
		naiveOut, naiveSt, nv := runNaive(cfg, ops)

		for i := range ops {
			if realOut[i] != naiveOut[i] {
				t.Fatalf("seed=%d op#%d %+v: 真实=%+v 朴素=%+v", seed, i, ops[i], realOut[i], naiveOut[i])
			}
			t.Logf("seed=%d #%d 输入=%+v => 输出=%+v 依据=与朴素模拟逐步一致", seed, i, ops[i], realOut[i])
		}
		if realSt != naiveSt {
			t.Fatalf("seed=%d stats 不一致:\n真实=%+v\n朴素=%+v", seed, realSt, naiveSt)
		}
		checkInvariants(t, realSt, nv.c, cfg.K, cfg.Cm)

		// 确定性：相同序列在全新实例上重放，结果与统计必须完全相同。
		replayOut, replaySt := runReal(t, cfg, ops)
		for i := range replayOut {
			if replayOut[i] != realOut[i] {
				t.Fatalf("seed=%d 重放不确定 op#%d: %+v != %+v", seed, i, replayOut[i], realOut[i])
			}
		}
		if replaySt != realSt {
			t.Fatalf("seed=%d 重放 stats 不一致: %+v != %+v", seed, replaySt, realSt)
		}
	}
}
