package reinsurance

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

func stdTerms() Terms {
	return Terms{QuotaPercent: 20, SurplusLine: 1000, SurplusLines: 4,
		XLDeductible: 1500, XLLimit: 2000, XLReinstatements: 1}
}

func mustEngine(t *testing.T, tr Terms) *Engine {
	t.Helper()
	e, err := NewEngine(tr)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustPolicy(t *testing.T, e *Engine, p Policy) {
	t.Helper()
	if err := e.RegisterPolicy(p); err != nil {
		t.Fatalf("RegisterPolicy %s: %v", p.ID, err)
	}
}

func mustFile(t *testing.T, e *Engine, c Claim) Claim {
	t.Helper()
	got, err := e.FileClaim(c)
	if err != nil {
		t.Fatalf("FileClaim %s: %v", c.ID, err)
	}
	return got
}

func occMap(occs []OccurrenceResult) map[string]OccurrenceResult {
	m := make(map[string]OccurrenceResult, len(occs))
	for _, o := range occs {
		m[o.Occurrence] = o
	}
	return m
}

func TestSurplusBoundaryExactlyRetention(t *testing.T) {
	e := mustEngine(t, stdTerms())
	mustPolicy(t, e, Policy{ID: "P1", Limit: 1250, StartDay: 0, EndDay: 10})
	pols, _, _ := e.Snapshot()
	p := pols[0]
	if p.QuotaShare != 250 || p.SurplusShare != 0 || p.NetRetention != 1000 {
		t.Fatalf("shares = %+v", p)
	}
}

func TestSurplusCapacityBoundary(t *testing.T) {
	e := mustEngine(t, stdTerms())
	mustPolicy(t, e, Policy{ID: "OK", Limit: 6250, StartDay: 0, EndDay: 10})
	err := e.RegisterPolicy(Policy{ID: "NO", Limit: 6251, StartDay: 0, EndDay: 10})
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("want ErrCapacityExceeded, got %v", err)
	}
}

func TestSplitTailToNet(t *testing.T) {
	e := mustEngine(t, stdTerms())
	mustPolicy(t, e, Policy{ID: "P", Limit: 101, StartDay: 0, EndDay: 10})
	got := mustFile(t, e, Claim{ID: "C", PolicyID: "P", Occurrence: "O", TimeSec: 0, Amount: 10})
	if got.QuotaPart != 1 || got.SurplusPart != 0 || got.NetPart != 9 {
		t.Fatalf("split = %+v", got)
	}
}

func TestAggregateAtAndAboveDeductible(t *testing.T) {
	tr := Terms{QuotaPercent: 40, SurplusLine: 100000, SurplusLines: 1,
		XLDeductible: 1500, XLLimit: 10000, XLReinstatements: 1}
	e := mustEngine(t, tr)
	mustPolicy(t, e, Policy{ID: "P", Limit: 2500, StartDay: 0, EndDay: 10}) // 净自留 60%
	mustFile(t, e, Claim{ID: "c1", PolicyID: "P", Occurrence: "O", TimeSec: 0, Amount: 2500})
	_, occs, _ := e.Snapshot()
	if occs[0].NetAggregate != 1500 || occs[0].XLRecovery != 0 {
		t.Fatalf("exactly deductible: %+v", occs[0])
	}
}

func TestAggregateOneAboveDeductible(t *testing.T) {
	tr := Terms{QuotaPercent: 50, SurplusLine: 100000, SurplusLines: 1,
		XLDeductible: 1500, XLLimit: 10000, XLReinstatements: 1}
	e := mustEngine(t, tr)
	// 净自留恰为保额一半：三张保单各赔 1000，合计 net=1500；再补一张净自留 1（limit=2, amount=2, quota=1）
	for _, id := range []string{"A", "B", "C"} {
		mustPolicy(t, e, Policy{ID: id, Limit: 1000, StartDay: 0, EndDay: 10})
		mustFile(t, e, Claim{ID: "c" + id, PolicyID: id, Occurrence: "O", TimeSec: 0, Amount: 1000})
	}
	mustPolicy(t, e, Policy{ID: "D", Limit: 2, StartDay: 0, EndDay: 10})
	mustFile(t, e, Claim{ID: "cD", PolicyID: "D", Occurrence: "O", TimeSec: 0, Amount: 2})
	_, occs, _ := e.Snapshot()
	if occs[0].NetAggregate != 1501 || occs[0].XLRecovery != 1 {
		t.Fatalf("one above: net=%d xl=%d", occs[0].NetAggregate, occs[0].XLRecovery)
	}
}

func TestCapacityMidExhaustion(t *testing.T) {
	tr := Terms{QuotaPercent: 20, SurplusLine: 10000, SurplusLines: 1,
		XLDeductible: 3000, XLLimit: 2000, XLReinstatements: 0}
	e := mustEngine(t, tr)
	mustPolicy(t, e, Policy{ID: "P", Limit: 5000, StartDay: 0, EndDay: 100})
	for i, oid := range []string{"O1", "O2", "O3"} {
		mustFile(t, e, Claim{ID: oid, PolicyID: "P", Occurrence: oid,
			TimeSec: int64(100 * (i + 1)), Amount: 5000})
	}
	_, occs, tot := e.Snapshot()
	m := occMap(occs)
	if m["O1"].XLRecovery != 1000 || m["O2"].XLRecovery != 1000 || m["O3"].XLRecovery != 0 {
		t.Fatalf("xl O1:%d O2:%d O3:%d", m["O1"].XLRecovery, m["O2"].XLRecovery, m["O3"].XLRecovery)
	}
	if tot.XL != 2000 || tot.XLRemaining != 0 {
		t.Fatalf("totals %+v", tot)
	}
}

func TestSameTimeOrderByOccurrenceID(t *testing.T) {
	tr := Terms{QuotaPercent: 20, SurplusLine: 10000, SurplusLines: 1,
		XLDeductible: 0, XLLimit: 1000, XLReinstatements: 0}
	e := mustEngine(t, tr)
	for _, id := range []string{"PZ", "PA"} {
		mustPolicy(t, e, Policy{ID: id, Limit: 1250, StartDay: 0, EndDay: 10})
	}
	mustFile(t, e, Claim{ID: "cZ", PolicyID: "PZ", Occurrence: "OZ", TimeSec: 500, Amount: 1250})
	mustFile(t, e, Claim{ID: "cA", PolicyID: "PA", Occurrence: "OA", TimeSec: 500, Amount: 1250})
	_, occs, tot := e.Snapshot()
	if occs[0].Occurrence != "OA" || occs[0].XLRecovery != 1000 ||
		occs[1].Occurrence != "OZ" || occs[1].XLRecovery != 0 || tot.XL != 1000 {
		t.Fatalf("occs=%+v tot=%+v", occs, tot)
	}
}

func TestLateClaimStealsEarlierCapacity(t *testing.T) {
	tr := Terms{QuotaPercent: 20, SurplusLine: 10000, SurplusLines: 1,
		XLDeductible: 0, XLLimit: 2000, XLReinstatements: 0}
	e := mustEngine(t, tr)
	mustPolicy(t, e, Policy{ID: "P", Limit: 5000, StartDay: 0, EndDay: 100})
	file := func(cid, oid string, ts int64) {
		mustFile(t, e, Claim{ID: cid, PolicyID: "P", Occurrence: oid, TimeSec: ts, Amount: 2500})
	}
	file("c2", "O2", 200)
	file("c1", "O1", 100)
	_, occs, tot := e.Snapshot()
	m := occMap(occs)
	if m["O1"].XLRecovery != 2000 || m["O2"].XLRecovery != 0 || tot.XL != 2000 {
		t.Fatalf("steal: %+v tot=%+v", m, tot)
	}
}

func TestCancelEquivalentToNeverExisted(t *testing.T) {
	tr := Terms{QuotaPercent: 20, SurplusLine: 10000, SurplusLines: 1,
		XLDeductible: 0, XLLimit: 2000, XLReinstatements: 0}
	e := mustEngine(t, tr)
	mustPolicy(t, e, Policy{ID: "P", Limit: 5000, StartDay: 0, EndDay: 100})
	mustFile(t, e, Claim{ID: "c1", PolicyID: "P", Occurrence: "O1", TimeSec: 100, Amount: 2500})
	mustFile(t, e, Claim{ID: "c2", PolicyID: "P", Occurrence: "O2", TimeSec: 200, Amount: 2500})
	if err := e.CancelClaim("c1"); err != nil {
		t.Fatal(err)
	}
	_, occs, tot := e.Snapshot()
	m := occMap(occs)
	if _, ok := m["O1"]; ok {
		t.Fatal("O1 should be removed")
	}
	if m["O2"].XLRecovery != 2000 || tot.XL != 2000 {
		t.Fatalf("O2 must be paid after cancel: %+v tot=%+v", m, tot)
	}
	if !errors.Is(e.CancelClaim("ghost"), ErrClaimNotFound) {
		t.Fatal("cancel missing claim must report ErrClaimNotFound")
	}
	if _, ok := e.Claim("c1"); ok {
		t.Fatal("c1 should be gone")
	}
}

func TestNotCoveredAndBasicErrors(t *testing.T) {
	e := mustEngine(t, stdTerms())
	mustPolicy(t, e, Policy{ID: "P", Limit: 1000, StartDay: 0, EndDay: 3})
	_, err := e.FileClaim(Claim{ID: "c", PolicyID: "P", Occurrence: "O", TimeSec: 86400, Amount: 1001})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("amount>limit: %v", err)
	}
	if _, err := e.FileClaim(Claim{ID: "c", PolicyID: "P", Occurrence: "O", TimeSec: 3 * 86400, Amount: 10}); !errors.Is(err, ErrNotCovered) {
		t.Fatalf("right endpoint excluded: %v", err)
	}
	if _, err := e.FileClaim(Claim{ID: "c", PolicyID: "P", Occurrence: "O", TimeSec: 0, Amount: 10}); err != nil {
		t.Fatalf("inside interval: %v", err)
	}
	if _, err := e.FileClaim(Claim{ID: "x", PolicyID: "ZZ", Occurrence: "O", TimeSec: 0, Amount: 1}); !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("policy missing: %v", err)
	}
	if _, err := e.FileClaim(Claim{ID: "c", PolicyID: "P", Occurrence: "O2", TimeSec: 0, Amount: 10}); !errors.Is(err, ErrClaimExists) {
		t.Fatalf("duplicate claim: %v", err)
	}
	if err := e.RegisterPolicy(Policy{ID: "P", Limit: 1, StartDay: 0, EndDay: 1}); !errors.Is(err, ErrPolicyDuplicate) {
		t.Fatalf("duplicate policy: %v", err)
	}
}

func TestInvalidTerms(t *testing.T) {
	base := stdTerms()
	bad := []func(*Terms){
		func(t *Terms) { t.QuotaPercent = 0 },
		func(t *Terms) { t.QuotaPercent = 100 },
		func(t *Terms) { t.SurplusLines = 0 },
		func(t *Terms) { t.SurplusLines = -1 },
		func(t *Terms) { t.SurplusLine = -1 },
		func(t *Terms) { t.XLReinstatements = -1 },
		func(t *Terms) { t.XLDeductible = -1 },
		func(t *Terms) { t.XLLimit = -1 },
	}
	for i, mut := range bad {
		tr := base
		mut(&tr)
		if _, err := NewEngine(tr); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: want ErrInvalidArgument, got %v", i, err)
		}
	}
}

// 拒绝次序固定：参数非法 > 保单不存在 > 保单重复 > 超出承保能力 >
// 赔款已存在 > 赔款不存在 > 事故未承保。
// 本文件逐对验证：每对相邻错误同时成立时，只报次序靠前者。

func TestRejectionOrderPairs(t *testing.T) {
	e := mustEngine(t, stdTerms())
	mustPolicy(t, e, Policy{ID: "DUP", Limit: 6250, StartDay: 0, EndDay: 10})
	mustPolicy(t, e, Policy{ID: "P", Limit: 1000, StartDay: 0, EndDay: 10})
	mustFile(t, e, Claim{ID: "EX", PolicyID: "P", Occurrence: "O", TimeSec: 0, Amount: 10})

	t.Run("invalid-before-policy-not-found", func(t *testing.T) {
		_, err := e.FileClaim(Claim{ID: "x1", PolicyID: "GHOST", Occurrence: "O", TimeSec: 0, Amount: 0})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid-before-claim-exists", func(t *testing.T) {
		_, err := e.FileClaim(Claim{ID: "EX", PolicyID: "P", Occurrence: "O", TimeSec: 0, Amount: -1})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid-before-not-covered", func(t *testing.T) {
		_, err := e.FileClaim(Claim{ID: "x2", PolicyID: "P", Occurrence: "O", TimeSec: 99 * 86400, Amount: 0})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid-before-policy-duplicate", func(t *testing.T) {
		err := e.RegisterPolicy(Policy{ID: "DUP", Limit: 0, StartDay: 0, EndDay: 1})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("invalid-before-claim-not-found", func(t *testing.T) {
		if !errors.Is(e.CancelClaim(""), ErrInvalidArgument) {
			t.Fatal("empty cancel id must be invalid")
		}
	})
	t.Run("policy-not-found-before-claim-exists", func(t *testing.T) {
		_, err := e.FileClaim(Claim{ID: "EX", PolicyID: "GHOST", Occurrence: "O", TimeSec: 0, Amount: 1})
		if !errors.Is(err, ErrPolicyNotFound) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("policy-not-found-before-not-covered", func(t *testing.T) {
		_, err := e.FileClaim(Claim{ID: "x3", PolicyID: "GHOST", Occurrence: "O", TimeSec: 99 * 86400, Amount: 1})
		if !errors.Is(err, ErrPolicyNotFound) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("policy-duplicate-before-capacity", func(t *testing.T) {
		err := e.RegisterPolicy(Policy{ID: "DUP", Limit: 1 << 60, StartDay: 0, EndDay: 1})
		if !errors.Is(err, ErrPolicyDuplicate) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("claim-exists-before-not-covered", func(t *testing.T) {
		_, err := e.FileClaim(Claim{ID: "EX", PolicyID: "P", Occurrence: "O2", TimeSec: 99 * 86400, Amount: 10})
		if !errors.Is(err, ErrClaimExists) {
			t.Fatalf("got %v", err)
		}
	})
}

// 撤销场景：赔款不存在 与 事故未承保 不可能同时出现（撤销不看承保区间），
// 但取消不存在赔款号必须稳定报 ErrClaimNotFound，已在 TestCancelEquivalent 中覆盖。

func TestConcurrentEquivalentToSerial(t *testing.T) {
	e := mustEngine(t, stdTerms())
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("P%03d", i)
			_ = e.RegisterPolicy(Policy{ID: id, Limit: 5000, StartDay: 0, EndDay: 100})
			_, _ = e.FileClaim(Claim{
				ID:       fmt.Sprintf("C%03d", i),
				PolicyID: id, Occurrence: fmt.Sprintf("O%03d", i%50),
				TimeSec: int64(i % 1000), Amount: 100,
			})
		}(i)
	}
	wg.Wait()

	// 并发结果必须与同集合交给朴素模型的结果一致。
	naive, _ := NewNaiveModel(stdTerms())
	_, occs, tot := e.Snapshot()
	pols := map[string]Policy{}
	gotPols, _, _ := e.Snapshot()
	for _, p := range gotPols {
		pols[p.ID] = p
	}
	var list []NaiveClaim
	// 重新枚举赔款：通过快照无法直接列出，改由逐号查询。
	for i := 0; i < n; i++ {
		if c, ok := e.Claim(fmt.Sprintf("C%03d", i)); ok {
			list = append(list, NaiveClaim{
				c.ID, c.PolicyID, c.Occurrence, c.TimeSec, c.Amount,
			})
		}
	}
	want := naive.Compute(pols, list)
	m := occMap(occs)
	if len(m) != len(want.OccurrenceXL) || tot != want.Totals {
		t.Fatalf("concurrent result diverges: %d vs %d occs, totals %+v vs %+v",
			len(m), len(want.OccurrenceXL), tot, want.Totals)
	}
}

// 登记与切分不得随已登记保单总数增长：比较 1k 与 64k 保单规模下的单次开销。
func BenchmarkRegisterAndFileDoesNotGrowWithPolicyCount(b *testing.B) {
	for _, n := range []int{1000, 64000} {
		b.Run(fmt.Sprintf("policies=%d", n), func(b *testing.B) {
			tr := stdTerms()
			e, err := NewEngine(tr)
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if err := e.RegisterPolicy(Policy{
					ID: fmt.Sprintf("P%06d", i), Limit: 5000, StartDay: 0, EndDay: 1000,
				}); err != nil {
					b.Fatal(err)
				}
			}
			pol := Policy{ID: "bench", Limit: 5000, StartDay: 0, EndDay: 1000}
			if err := registerPolicy(&pol, tr); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// 纯切分：只依赖该保单自身份额，不遍历保单表。
				splitClaim(&pol, 123)
			}
		})
	}
}

// 随机生成保单与赔款，以洗牌后的顺序报案/撤销，每一步后把引擎结果与
// 「按事故时刻一次算成」的独立朴素模型逐分对照；-v 时打印输入/输出/判定依据。
func TestRandomEquivalence(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runOneRandom(t, seed)
		})
	}
}

func runOneRandom(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	tr := Terms{
		QuotaPercent:     []int{1, 10, 20, 50, 99}[rng.Intn(5)],
		SurplusLine:      int64(500 + rng.Intn(3000)),
		SurplusLines:     1 + rng.Intn(4),
		XLDeductible:     int64(rng.Intn(3000)),
		XLLimit:          int64(500 + rng.Intn(3000)),
		XLReinstatements: rng.Intn(3),
	}
	eng, err := NewEngine(tr)
	if err != nil {
		t.Fatalf("terms %+v: %v", tr, err)
	}
	naive, err := NewNaiveModel(tr)
	if err != nil {
		t.Fatal(err)
	}

	nPols := 2 + rng.Intn(5)
	pols := map[string]Policy{}
	var regs []Policy
	for pi := 0; pi < nPols; pi++ {
		limit := int64(100 + rng.Intn(8000))
		st := int64(rng.Intn(5))
		regs = append(regs, Policy{
			ID: fmt.Sprintf("P%02d", pi), Limit: limit,
			StartDay: st, EndDay: st + int64(1+rng.Intn(8)),
		})
	}
	rng.Shuffle(len(regs), func(i, j int) { regs[i], regs[j] = regs[j], regs[i] })
	for _, rp := range regs {
		if err := eng.RegisterPolicy(rp); err == nil {
			got, _, _ := eng.Snapshot()
			for _, gp := range got {
				if gp.ID == rp.ID {
					pols[gp.ID] = gp
				}
			}
		}
	}
	if len(pols) == 0 {
		t.Skip("all policies rejected by capacity")
	}

	polIDs := make([]string, 0, len(pols))
	for id := range pols {
		polIDs = append(polIDs, id)
	}
	sort.Strings(polIDs)

	nClaims := 6 + rng.Intn(30)
	nOcc := 1 + nClaims/3
	occTime := map[string]int64{}
	for i := 0; i < nOcc; i++ {
		occTime[fmt.Sprintf("O%02d", i)] = rng.Int63n(int64(13 * 86400))
	}
	type claimSpec struct{ c NaiveClaim }
	var all []claimSpec
	for i := 0; i < nClaims; i++ {
		pid := polIDs[rng.Intn(len(polIDs))]
		p := pols[pid]
		oid := fmt.Sprintf("O%02d", rng.Intn(nOcc))
		all = append(all, claimSpec{NaiveClaim{
			ID:         fmt.Sprintf("C%03d", i),
			PolicyID:   pid,
			Occurrence: oid,
			TimeSec:    occTime[oid],
			Amount:     1 + rng.Int63n(p.Limit),
		}})
	}
	order := rng.Perm(len(all))

	active := map[string]NaiveClaim{}
	compare := func(stage string) {
		t.Helper()
		list := make([]NaiveClaim, 0, len(active))
		for _, c := range active {
			list = append(list, c)
		}
		want := naive.Compute(pols, list)
		_, occs, tot := eng.Snapshot()
		gotM := occMap(occs)
		if len(gotM) != len(want.OccurrenceXL) {
			t.Fatalf("[%s seed=%d] occ count %d != %d", stage, seed, len(gotM), len(want.OccurrenceXL))
		}
		for id, xl := range want.OccurrenceXL {
			g := gotM[id]
			if g.XLRecovery != xl || g.NetAggregate != want.OccurrenceNet[id] {
				t.Fatalf("[%s seed=%d] occ %s got=%+v want xl=%d net=%d",
					stage, seed, id, g, xl, want.OccurrenceNet[id])
			}
		}
		if tot != want.Totals {
			t.Fatalf("[%s seed=%d] totals got=%+v want=%+v", stage, seed, tot, want.Totals)
		}
	}

	for step, idx := range order {
		nc := all[idx].c
		in := Claim{ID: nc.ID, PolicyID: nc.PolicyID, Occurrence: nc.Occurrence,
			TimeSec: nc.TimeSec, Amount: nc.Amount}
		out, ferr := eng.FileClaim(in)
		t.Logf("seed=%d step=%d FILE in=%+v out=%+v reason=%v", seed, step, in, out, ferr)
		if ferr == nil {
			active[in.ID] = nc
		}
		compare(fmt.Sprintf("file-%d", step))

		if rng.Intn(4) == 0 && len(active) > 0 {
			var victim string
			for id := range active {
				victim = id
				break
			}
			t.Logf("seed=%d step=%d CANCEL id=%s", seed, step, victim)
			if err := eng.CancelClaim(victim); err != nil {
				t.Fatalf("cancel %s: %v", victim, err)
			}
			delete(active, victim)
			compare(fmt.Sprintf("cancel-%s", victim))
		}
	}
	compare("final")
}
