package alloc_test

import (
	"fmt"
	"strings"
	"testing"

	"ontology/alloc"
	"ontology/decider"
	"ontology/node"
)

func buildPair(t *testing.T, L, H int) (*node.Cluster, *sim) {
	t.Helper()
	c, err := node.New(L, H)
	if err != nil {
		t.Fatal(err)
	}
	return c, newSim(L, H)
}

func pairAdd(t *testing.T, c *node.Cluster, m *sim, id, zone string, total int64) {
	t.Helper()
	if err := c.AddNode(id, zone, total); err != nil {
		t.Fatal(err)
	}
	m.addNode(id, zone, total)
}

func pairOther(t *testing.T, c *node.Cluster, m *sim, id string, v int64) {
	t.Helper()
	if err := c.SetOther(id, v); err != nil {
		t.Fatal(err)
	}
	m.setOther(id, v)
}

func pairExclude(t *testing.T, c *node.Cluster, m *sim, id string, on bool) {
	t.Helper()
	if err := c.SetExclude(id, on); err != nil {
		t.Fatal(err)
	}
	m.setExclude(id, on)
}

func pairIndex(t *testing.T, c *node.Cluster, m *sim, name string, s, r int, size int64) {
	t.Helper()
	if err := c.CreateIndex(name, s, r, size); err != nil {
		t.Fatal(err)
	}
	m.createIndex(name, s, r, size)
}

func pairReroute(t *testing.T, c *node.Cluster, m *sim) alloc.Result {
	t.Helper()
	res, err := alloc.New(c).Reroute()
	if err != nil {
		t.Fatal(err)
	}
	sa, sm := m.reroute()
	if itemsKey(res.Assigned) != itemsKey(sa) || itemsKey(res.Moved) != itemsKey(sm) {
		t.Fatalf("reroute mismatch:\n real A=%v\n sim  A=%v\n real M=%v\n sim  M=%v\n basis:\n%s",
			res.Assigned, sa, res.Moved, sm, strings.Join(m.log, "\n"))
	}
	return res
}

func findItem(xs []alloc.Item, index string, s int, primary bool) (alloc.Item, bool) {
	for _, x := range xs {
		if x.Index == index && x.Shard == s && x.Primary == primary {
			return x, true
		}
	}
	return alloc.Item{}, false
}

// 排除节点全量迁出（单区域，c=1，无 D3 约束）。
func TestExcludedEvictAll(t *testing.T) {
	c, m := buildPair(t, 80, 90)
	pairAdd(t, c, m, "n1", "z1", 100)
	pairAdd(t, c, m, "n2", "z1", 100)
	pairIndex(t, c, m, "x", 1, 0, 30)
	pairReroute(t, c, m)
	pairExclude(t, c, m, "n1", true)
	res := pairReroute(t, c, m)
	it, ok := findItem(res.Moved, "x", 0, true)
	if !ok || it.From != "n1" || it.To != "n2" {
		t.Fatalf("want x primary n1->n2, got %+v", res.Moved)
	}
}

// 排除节点全量迁出但迁不动：另一个区域的唯一节点已持同份（D2）。
func TestExcludedEvictStuck(t *testing.T) {
	c, m := buildPair(t, 80, 90)
	pairAdd(t, c, m, "n1", "z1", 100)
	pairAdd(t, c, m, "n2", "z2", 100)
	pairIndex(t, c, m, "x", 1, 1, 30)
	pairReroute(t, c, m) // 主 n1(z1)，副 n2(z2)
	pairExclude(t, c, m, "n2", true)
	res := pairReroute(t, c, m)
	if len(res.Moved) != 0 {
		t.Fatalf("replica must stay on excluded n2 (D2), got %v", res.Moved)
	}
}

// 超高水位逐份迁出，一旦不再大于即停。
func TestOverHighStopWhenBelow(t *testing.T) {
	c, m := buildPair(t, 80, 90)
	pairAdd(t, c, m, "n1", "z1", 100)
	pairAdd(t, c, m, "n2", "z1", 100)
	pairIndex(t, c, m, "a", 1, 0, 10)
	pairIndex(t, c, m, "b", 1, 0, 10)
	// 初始排除 n2，迫使 a、b 两份主都落 n1。
	pairExclude(t, c, m, "n2", true)
	pairReroute(t, c, m)
	pairExclude(t, c, m, "n2", false)
	pairOther(t, c, m, "n1", 75) // used=95 > 90
	res := pairReroute(t, c, m)
	// 迁出一份后 n1 used=85 ≤ 90 即停：恰好 1 次迁移。
	if len(res.Moved) != 1 {
		t.Fatalf("want exactly 1 move (stop once <= H), got %v", res.Moved)
	}
	if res.Moved[0].From != "n1" || res.Moved[0].To != "n2" {
		t.Fatalf("move n1->n2 expected, got %+v", res.Moved[0])
	}
}

// 迁出时区域计数先减 1：源与目标同区域，不减 1 则目标被 D3 否决。
func TestRelocationZoneCountDecremented(t *testing.T) {
	c, m := buildPair(t, 80, 90)
	pairAdd(t, c, m, "n1", "z1", 100) // 主
	pairAdd(t, c, m, "n2", "z1", 100)
	pairAdd(t, c, m, "n3", "z2", 100) // 副本先落这里
	pairAdd(t, c, m, "n4", "z2", 100) // 迁出目标
	pairIndex(t, c, m, "x", 1, 1, 30)
	pairReroute(t, c, m) // c=2,z=2,limit1：主 n1，副 n3
	// n3 抬高到超高水位：30+65=95>90；迁出评估一律低水位。
	pairOther(t, c, m, "n3", 65)
	res := pairReroute(t, c, m)
	it, ok := findItem(res.Moved, "x", 0, false)
	if !ok {
		t.Fatalf("replica should move off n3, moves=%v basis=%s",
			res.Moved, strings.Join(m.log, "\n"))
	}
	if it.From != "n3" || it.To != "n4" {
		t.Fatalf("want n3->n4 (zone count decremented first), got %+v", it)
	}
}

// 主未就绪：主被 D4 否决时，副本本轮整体跳过（Assigned 为空）。
func TestPrimaryNotReadySkipsReplicas(t *testing.T) {
	c, m := buildPair(t, 80, 90)
	pairAdd(t, c, m, "n1", "z1", 10)
	pairIndex(t, c, m, "x", 1, 2, 50)
	res := pairReroute(t, c, m)
	if len(res.Assigned) != 0 {
		t.Fatalf("want nothing assigned, got %v", res.Assigned)
	}
}

// 主按高水位恰等通过：used+size == H×total 不否决。
func TestPrimaryHighWaterEquality(t *testing.T) {
	c, m := buildPair(t, 80, 90)
	pairAdd(t, c, m, "n1", "z1", 100)
	pairOther(t, c, m, "n1", 40)
	pairIndex(t, c, m, "x", 1, 0, 50) // 40+50=90 恰等 H
	res := pairReroute(t, c, m)
	if len(res.Assigned) != 1 || res.Assigned[0].To != "n1" {
		t.Fatalf("primary should pass at exact H, got %v", res.Assigned)
	}
}

// Explain 与朴素模拟器逐节点一致。
func TestExplainMatchesSim(t *testing.T) {
	c, m := buildPair(t, 80, 90)
	pairAdd(t, c, m, "n1", "z1", 100)
	pairAdd(t, c, m, "n2", "z1", 100)
	pairAdd(t, c, m, "n3", "z2", 100)
	pairOther(t, c, m, "n3", 55)
	pairIndex(t, c, m, "x", 2, 2, 30)

	for _, primary := range []bool{true, false} {
		got, err := alloc.New(c).Explain("x", 1, primary)
		if err != nil {
			t.Fatal(err)
		}
		want, notReady, _ := m.explain("x", 1, primary)
		if len(got.Verdicts) != len(want) {
			t.Fatalf("verdict count %d vs %d", len(got.Verdicts), len(want))
		}
		for i := range want {
			if got.Verdicts[i].Node != want[i].Node ||
				got.Verdicts[i].Rule != want[i].Rule ||
				got.Verdicts[i].Pass != want[i].Pass {
				t.Fatalf("explain primary=%v node=%s: got %+v want %+v",
					primary, want[i].Node, got.Verdicts[i], want[i])
			}
		}
		if got.PrimaryNotReady != notReady {
			t.Fatalf("PrimaryNotReady got %v want %v", got.PrimaryNotReady, notReady)
		}
	}
}

// evals 稳态：全部就位、无排除非空、无超高水位时规则评估 0 次，与份数无关。
func TestEvalsZeroAtSteadyState(t *testing.T) {
	for _, tc := range []struct {
		name       string
		indexes    int
		shards     int
		replicas   int
		wantCopies int
	}{
		{"100 copies", 1, 20, 4, 100}, // 20 * 5 = 100
		{"10000 copies", 27, 64, 5, 27 * 64 * 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := node.New(80, 90)
			// 20 个节点同区域（z=1，区域不构成约束），海量磁盘。
			for i := 0; i < 20; i++ {
				if err := c.AddNode(fmt.Sprintf("n%02d", i), "z1", 1_000_000_000_000); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < tc.indexes; i++ {
				name := fmt.Sprintf("idx%02d", i)
				if err := c.CreateIndex(name, tc.shards, tc.replicas, 1024); err != nil {
					t.Fatal(err)
				}
			}
			a := alloc.New(c)
			totalCopies := 0
			for round := 0; round < 20 && totalCopies < tc.wantCopies; round++ {
				res, err := a.Reroute()
				if err != nil {
					t.Fatal(err)
				}
				totalCopies += len(res.Assigned)
			}
			if totalCopies != tc.wantCopies {
				t.Fatalf("assigned %d, want %d", totalCopies, tc.wantCopies)
			}
			// 稳态再跑：必须 0 次评估。
			before := alloc.Evals()
			res, err := a.Reroute()
			if err != nil {
				t.Fatal(err)
			}
			if alloc.Evals() != 0 {
				t.Fatalf("steady-state evals=%d, want 0 (copies=%d)", alloc.Evals(), tc.wantCopies)
			}
			if len(res.Assigned) != 0 || len(res.Moved) != 0 {
				t.Fatalf("steady state should be no-op, got A=%v M=%v", res.Assigned, res.Moved)
			}
			_ = before
		})
	}
}

var _ = decider.Pass
