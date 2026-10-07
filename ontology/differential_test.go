package ontology

import (
	"math/rand"
	"testing"
)

// op 是差分测试的统一操作记录；重放同一 []op 必须得到完全相同结果。
type op struct {
	kind  string // "create" | "delete" | "batchBE" | "batchAON"
	item  BatchItem
	items []BatchItem
}

// TestRandomDifferentialVsNaive 在大量随机创建/删除/批量导入序列下，
// 将正式实现逐条与朴素全量重算模型对照：每条结论、批结果与最终链接集合必须一致。
// 同一操作序列在新的正式实例上重放，必须得到完全相同的链接集合与基数状态。
func TestRandomDifferentialVsNaive(t *testing.T) {
	const (
		seeds    = 40
		opCount  = 300
		nSources = 6
		nTargets = 6
	)

	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s, m := mustRegister(t)
		ops := make([]op, 0, opCount)

		randItem := func() BatchItem {
			// 80% 走 LT1（src cap 2 / tgt cap 1，易饱和），20% 走 LT2（ExactlyOne/∞）
			if rng.Intn(5) == 0 {
				return BatchItem{
					LinkType: "LT2",
					Pair:     Pair{"u" + itoa2(1+rng.Intn(2)), "v" + itoa2(1+rng.Intn(3))},
				}
			}
			return BatchItem{
				LinkType: "LT1",
				Pair:     Pair{"s" + itoa2(1+rng.Intn(nSources)), "t" + itoa2(1+rng.Intn(nTargets))},
			}
		}

		invalidPool := []BatchItem{
			{LinkType: "NOPE", Pair: Pair{"s1", "t1"}},   // 未知类型
			{LinkType: "LT1", Pair: Pair{"ghost", "t1"}}, // 实例不存在
			{LinkType: "LT1", Pair: Pair{"s1", "v1"}},    // 类型不匹配
		}

		for step := 0; step < opCount; step++ {
			r := rng.Float64()
			switch {
			case r < 0.45:
				item := randItem()
				if rng.Intn(10) == 0 {
					item = invalidPool[rng.Intn(len(invalidPool))]
				}
				ops = append(ops, op{kind: "create", item: item})
				got := KindOf(s.CreateLink(item.LinkType, item.Pair))
				want := m.Create(item)
				if got != want {
					t.Fatalf("seed=%d step=%d create %v: 实际=%s 朴素判定=%s", seed, step, item, got, want)
				}
			case r < 0.7:
				item := randItem()
				ops = append(ops, op{kind: "delete", item: item})
				got := KindOf(s.DeleteLink(item.LinkType, item.Pair))
				want := m.Delete(item)
				if got != want {
					t.Fatalf("seed=%d step=%d delete %v: 实际=%s 朴素判定=%s", seed, step, item, got, want)
				}
			case r < 0.86:
				items := randomBatch(rng, randItem, invalidPool)
				ops = append(ops, op{kind: "batchBE", items: items})
				got := s.ImportLinks(BestEffort, items)
				want := m.Import(BestEffort, items)
				if !resultsEqual(got, want) {
					t.Fatalf("seed=%d step=%d BE 批结果不一致\n输入=%v\n实际=%v\n朴素=%v",
						seed, step, fmtItems(items), fmtResults(got), fmtResults(want))
				}
			default:
				items := randomBatch(rng, randItem, invalidPool)
				ops = append(ops, op{kind: "batchAON", items: items})
				got := s.ImportLinks(AllOrNothing, items)
				want := m.Import(AllOrNothing, items)
				if !resultsEqual(got, want) {
					t.Fatalf("seed=%d step=%d AON 批结果不一致\n输入=%v\n实际=%v\n朴素=%v",
						seed, step, fmtItems(items), fmtResults(got), fmtResults(want))
				}
			}

			if !sameSnapshot(s.Snapshot(), m.snapshot()) {
				t.Fatalf("seed=%d step=%d 链接集合发散\n实际=%v\n朴素=%v",
					seed, step, s.Snapshot(), m.snapshot())
			}
		}

		finalWanted := m.snapshot()
		t.Logf("seed=%d ops=%d 最终链接数=%d", seed, len(ops), len(finalWanted))

		// 重放确定性：在全新实例上按同一序列串行重放，链接集合必须逐字节相同。
		s2, m2 := mustRegister(t)
		replay(t, s2, ops)
		if !sameSnapshot(s2.Snapshot(), finalWanted) {
			t.Fatalf("seed=%d 重放链接集合不一致", seed)
		}
		// 朴素模型自身重放也是同一集合（双保险）。
		replayNaive(m2, ops)
		if !sameSnapshot(m2.snapshot(), finalWanted) {
			t.Fatalf("seed=%d 朴素重放不一致", seed)
		}
		// 账本计数与全量集合逐条对账。
		assertLedgerMatchesSet(t, s2)
	}
}

func randomBatch(rng *rand.Rand, randItem func() BatchItem, invalid []BatchItem) []BatchItem {
	n := 1 + rng.Intn(6)
	items := make([]BatchItem, n)
	for i := range items {
		items[i] = randItem()
		if rng.Intn(8) == 0 {
			items[i] = invalid[rng.Intn(len(invalid))]
		}
	}
	// 以一定概率人为制造批内重复。
	if n >= 2 && rng.Intn(2) == 0 {
		items[n-1] = items[0]
	}
	return items
}

func replay(t *testing.T, s *Service, ops []op) {
	t.Helper()
	for _, o := range ops {
		switch o.kind {
		case "create":
			_ = s.CreateLink(o.item.LinkType, o.item.Pair)
		case "delete":
			_ = s.DeleteLink(o.item.LinkType, o.item.Pair)
		case "batchBE":
			s.ImportLinks(BestEffort, o.items)
		case "batchAON":
			s.ImportLinks(AllOrNothing, o.items)
		}
	}
}

func replayNaive(m *naiveModel, ops []op) {
	for _, o := range ops {
		switch o.kind {
		case "create":
			m.Create(o.item)
		case "delete":
			m.Delete(o.item)
		case "batchBE":
			m.Import(BestEffort, o.items)
		case "batchAON":
			m.Import(AllOrNothing, o.items)
		}
	}
}

// assertLedgerMatchesSet 用全量集合重新统计，验证账本两侧计数全部吻合。
func assertLedgerMatchesSet(t *testing.T, s *Service) {
	t.Helper()
	counts := map[linkKeySide]int{}
	for _, l := range s.Snapshot() {
		counts[linkKeySide{l.LinkType, l.Source, true}]++
		counts[linkKeySide{l.LinkType, l.Target, false}]++
	}
	for c, n := range counts {
		var got int
		if c.source {
			got = s.UsedSource(c.linkType, c.instance)
		} else {
			got = s.UsedTarget(c.linkType, c.instance)
		}
		if got != n {
			t.Fatalf("账本与集合不一致：%s/%s source=%v 账本=%d 集合重数=%d",
				c.linkType, c.instance, c.source, got, n)
		}
	}
}

type linkKeySide struct {
	linkType string
	instance string
	source   bool
}

// TestRandomTracePrinting 小规模固定种子，逐操作打印输入/实际输出/判定依据，
// 满足「测试过程中打印输入、实际输出与据以判定的依据」的可观察要求。
func TestRandomTracePrinting(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	s, m := mustRegister(t)

	pick := func() BatchItem {
		return BatchItem{
			LinkType: "LT1",
			Pair:     Pair{"s" + itoa2(1+rng.Intn(3)), "t" + itoa2(1+rng.Intn(3))},
		}
	}

	for step := 0; step < 25; step++ {
		if rng.Intn(4) == 0 {
			items := []BatchItem{pick(), pick(), pick()}
			mode := []BatchMode{BestEffort, AllOrNothing}[rng.Intn(2)]
			modeName := map[BatchMode]string{BestEffort: "尽力而为", AllOrNothing: "全有或全无"}[mode]
			want := m.Import(mode, items)
			got := s.ImportLinks(mode, items)
			t.Logf("[step %02d] 批量(%s) 输入=%v", step, modeName, fmtItems(items))
			t.Logf("           实际=%v 依据(朴素重算)=%v", fmtResults(got), fmtResults(want))
			if !resultsEqual(got, want) {
				t.Fatalf("step %d 批结果不一致", step)
			}
			continue
		}
		item := pick()
		if rng.Intn(2) == 0 {
			got := KindOf(s.CreateLink(item.LinkType, item.Pair))
			want := m.Create(item)
			t.Logf("[step %02d] 创建 输入=%v 实际=%s 依据(朴素重算)=%s", step, item.Pair, got, want)
			if got != want {
				t.Fatalf("step %d create 分歧 %s vs %s", step, got, want)
			}
		} else {
			got := KindOf(s.DeleteLink(item.LinkType, item.Pair))
			want := m.Delete(item)
			t.Logf("[step %02d] 删除 输入=%v 实际=%s 依据(朴素重算)=%s", step, item.Pair, got, want)
			if got != want {
				t.Fatalf("step %d delete 分歧 %s vs %s", step, got, want)
			}
		}
	}
	t.Logf("最终链接集合=%v", s.Snapshot())
	if !sameSnapshot(s.Snapshot(), m.snapshot()) {
		t.Fatalf("最终集合发散")
	}
}
