package alloc_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/alloc"
	"ontology/decider"
	"ontology/node"
)

// rule 把节点名映射到期望判定，便于表驱动断言 Explain。
func rules(vs []alloc.Verdict) map[string]decider.Reason {
	out := map[string]decider.Reason{}
	for _, v := range vs {
		out[v.Node] = v.Rule
	}
	return out
}

// scenario 用统一的 builder 风格步骤描述，避免手写多份样板。
type step struct {
	apply func(t *testing.T, c *node.Cluster, m *sim)
	desc  string
}

func addNode(id, zone string, total int64) step {
	return step{
		desc: fmt.Sprintf("AddNode(%s,%s,%d)", id, zone, total),
		apply: func(t *testing.T, c *node.Cluster, m *sim) {
			if err := c.AddNode(id, zone, total); err != nil {
				t.Fatalf("%s: %v", "AddNode", err)
			}
			m.addNode(id, zone, total)
		},
	}
}

func other(id string, v int64) step {
	return step{
		desc: fmt.Sprintf("SetOther(%s,%d)", id, v),
		apply: func(t *testing.T, c *node.Cluster, m *sim) {
			if err := c.SetOther(id, v); err != nil {
				t.Fatalf("SetOther: %v", err)
			}
			m.setOther(id, v)
		},
	}
}

func exclude(id string, on bool) step {
	return step{
		desc: fmt.Sprintf("SetExclude(%s,%v)", id, on),
		apply: func(t *testing.T, c *node.Cluster, m *sim) {
			if err := c.SetExclude(id, on); err != nil {
				t.Fatalf("SetExclude: %v", err)
			}
			m.setExclude(id, on)
		},
	}
}

func index(name string, s, r int, size int64) step {
	return step{
		desc: fmt.Sprintf("CreateIndex(%s,S=%d,r=%d,size=%d)", name, s, r, size),
		apply: func(t *testing.T, c *node.Cluster, m *sim) {
			if err := c.CreateIndex(name, s, r, size); err != nil {
				t.Fatalf("CreateIndex: %v", err)
			}
			m.createIndex(name, s, r, size)
		},
	}
}

// expect 断言一轮 Reroute 与朴素模拟一致，并可选断言具体落点。
type rerouteCheck struct {
	wantAssigned map[string]string // "name/s/P" -> node
	wantMoved    map[string]string // "name/s/P" -> "from->to"
}

func reroute(chk rerouteCheck) step {
	return step{
		desc: "Reroute()",
		apply: func(t *testing.T, c *node.Cluster, m *sim) {
			a := alloc.New(c)
			got, err := a.Reroute()
			if err != nil {
				t.Fatalf("Reroute: %v", err)
			}
			simA, simM := m.reroute()
			if itemsKey(got.Assigned) != itemsKey(simA) || itemsKey(got.Moved) != itemsKey(simM) {
				t.Fatalf("reroute mismatch\n got A=%v\n sim A=%v\n got M=%v\n sim M=%v",
					got.Assigned, simA, got.Moved, simM)
			}
			gotAssigned := map[string]string{}
			for _, it := range got.Assigned {
				gotAssigned[fmt.Sprintf("%s/%d/%v", it.Index, it.Shard, it.Primary)] = it.To
			}
			for k, v := range chk.wantAssigned {
				if gotAssigned[k] != v {
					t.Fatalf("assigned %s: want %s got %s (all=%v)", k, v, gotAssigned[k], gotAssigned)
				}
			}
			gotMoved := map[string]string{}
			for _, it := range got.Moved {
				gotMoved[fmt.Sprintf("%s/%d/%v", it.Index, it.Shard, it.Primary)] = it.From + "->" + it.To
			}
			for k, v := range chk.wantMoved {
				if gotMoved[k] != v {
					t.Fatalf("moved %s: want %s got %s (all=%v)", k, v, gotMoved[k], gotMoved)
				}
			}
		},
	}
}

func runScenario(t *testing.T, L, H int, steps []step) {
	t.Helper()
	c, err := node.New(L, H)
	if err != nil {
		t.Fatal(err)
	}
	m := newSim(L, H)
	for i, st := range steps {
		t.Run(fmt.Sprintf("%02d_%s", i, st.desc), func(t *testing.T) {
			st.apply(t, c, m)
		})
	}
}

func TestExampleFromSpec(t *testing.T) {
	// L=80 H=90；n1/n2 属 z1，n3 属 z2；total=100；x c=3 ceil(3/2)=2
	runScenario(t, 80, 90, []step{
		addNode("n1", "z1", 100),
		addNode("n2", "z1", 100),
		addNode("n3", "z2", 100),
		index("x", 1, 2, 30),
		reroute(rerouteCheck{wantAssigned: map[string]string{
			"x/0/true":  "n1",
			"x/0/false": "n3", // 两份副按放置次序分别落 n2、n3；映射只查最终唯一 key
		}}),
	})
	// 上一步的两份副本在 map key 相同，改为显式顺序断言：
	c, _ := node.New(80, 90)
	for _, a := range [][3]string{{"n1", "z1", "100"}, {"n2", "z1", "100"}, {"n3", "z2", "100"}} {
		must(t, c.AddNode(a[0], a[1], parseInt(a[2])))
	}
	must(t, c.CreateIndex("x", 1, 2, 30))
	res, err := alloc.New(c).Reroute()
	must(t, err)
	if len(res.Assigned) != 3 {
		t.Fatalf("want 3 assigned, got %v", res.Assigned)
	}
	want := []alloc.Item{
		{Index: "x", Shard: 0, Primary: true, To: "n1"},
		{Index: "x", Shard: 0, Primary: false, To: "n2"},
		{Index: "x", Shard: 0, Primary: false, To: "n3"},
	}
	for i := range want {
		if res.Assigned[i] != want[i] {
			t.Fatalf("assigned[%d]=%+v want %+v", i, res.Assigned[i], want[i])
		}
	}
}

func TestExplainD2D3D4AndEquality(t *testing.T) {
	c, _ := node.New(80, 90)
	m := newSim(80, 90)
	for _, a := range [][3]string{{"n1", "z1", "100"}, {"n2", "z1", "100"}, {"n3", "z2", "100"}} {
		must(t, c.AddNode(a[0], a[1], parseInt(a[2])))
		m.addNode(a[0], a[1], 100)
	}
	must(t, c.SetOther("n3", 55))
	m.setOther("n3", 55)
	must(t, c.CreateIndex("y", 1, 1, 30))
	m.createIndex("y", 1, 1, 30)

	a := alloc.New(c)
	res, err := a.Reroute()
	must(t, err)
	simA, simM := m.reroute()
	if itemsKey(res.Assigned) != itemsKey(simA) || itemsKey(res.Moved) != itemsKey(simM) {
		t.Fatalf("mismatch %v vs sim %v/%v", res, simA, simM)
	}
	if len(res.Assigned) != 1 || !res.Assigned[0].Primary || res.Assigned[0].To != "n1" {
		t.Fatalf("primary want n1, got %v", res.Assigned)
	}

	ex, err := a.Explain("y", 0, false)
	must(t, err)
	rs := rules(ex.Verdicts)
	if rs["n1"] != decider.D2SameShard || rs["n2"] != decider.D3ZoneAwareness ||
		rs["n3"] != decider.D4Disk || ex.PrimaryNotReady {
		t.Fatalf("explain: %+v notready=%v", rs, ex.PrimaryNotReady)
	}

	// n3 降到恰等 80：副本可放
	must(t, c.SetOther("n3", 50))
	m.setOther("n3", 50)
	res2, err := a.Reroute()
	must(t, err)
	simA2, simM2 := m.reroute()
	if itemsKey(res2.Assigned) != itemsKey(simA2) || itemsKey(res2.Moved) != itemsKey(simM2) {
		t.Fatalf("mismatch2 %v vs %v/%v", res2, simA2, simM2)
	}
	if len(res2.Assigned) != 1 || res2.Assigned[0].To != "n3" {
		t.Fatalf("replica want n3 at equality, got %v", res2.Assigned)
	}

	// n3=65 -> used 95 > 90 迁出；n1 D2、n2 D3、迁不动
	must(t, c.SetOther("n3", 65))
	m.setOther("n3", 65)
	res3, err := a.Reroute()
	must(t, err)
	_, simM3 := m.reroute()
	if itemsKey(res3.Moved) != itemsKey(simM3) {
		t.Fatalf("mismatch3 moved %v vs %v", res3.Moved, simM3)
	}
	if len(res3.Moved) != 0 {
		t.Fatalf("want no move, got %v", res3.Moved)
	}
}

func TestWatermarkPrimaryHighReplicaLow(t *testing.T) {
	// size=50: used 40 -> 主 (40+50)=90 恰等于 H 通过；副同样位置按 L=80 否决
	runScenario(t, 80, 90, []step{
		addNode("n1", "z1", 100),
		other("n1", 40),
		index("p", 1, 0, 50),
		reroute(rerouteCheck{wantAssigned: map[string]string{"p/0/true": "n1"}}),
	})
}

func TestZoneCountChangeAndCeil(t *testing.T) {
	// 2 区域、c=2、ceil=1：主 n1(z1)，副本不能进 z1（D3），只能 n3(z2)
	c, _ := node.New(80, 90)
	m := newSim(80, 90)
	for _, a := range [][3]string{{"n1", "z1", "1000"}, {"n2", "z1", "1000"}, {"n3", "z2", "1000"}} {
		must(t, c.AddNode(a[0], a[1], parseInt(a[2])))
		m.addNode(a[0], a[1], 1000)
	}
	must(t, c.CreateIndex("z", 1, 1, 10))
	m.createIndex("z", 1, 1, 10)
	res, err := alloc.New(c).Reroute()
	must(t, err)
	sa, sm := m.reroute()
	if itemsKey(res.Assigned) != itemsKey(sa) || itemsKey(res.Moved) != itemsKey(sm) {
		t.Fatalf("mismatch %v vs %v", res.Assigned, sa)
	}
	if res.Assigned[1].To != "n3" {
		t.Fatalf("replica must be in z2, got %s", res.Assigned[1].To)
	}
}

func TestExplainErrors(t *testing.T) {
	c, _ := node.New(80, 90)
	must(t, c.AddNode("n1", "z1", 100))
	must(t, c.CreateIndex("e", 2, 1, 10))
	a := alloc.New(c)
	if _, err := a.Explain("missing", 0, true); !errors.Is(err, node.ErrNotFound) {
		t.Fatalf("index missing: %v", err)
	}
	if _, err := a.Explain("e", 5, true); !errors.Is(err, node.ErrShardGone) {
		t.Fatalf("shard out of range want ErrShardGone, got %v", err)
	}
	if _, err := a.Explain("e", -1, true); !errors.Is(err, node.ErrShardGone) {
		t.Fatalf("negative shard: %v", err)
	}
}

func TestExplainPrimaryNotReady(t *testing.T) {
	c, _ := node.New(80, 90)
	must(t, c.AddNode("n1", "z1", 10)) // 任何放置都超水位 -> 主放不下
	must(t, c.CreateIndex("u", 1, 1, 50))
	ex, err := alloc.New(c).Explain("u", 0, false)
	must(t, err)
	if !ex.PrimaryNotReady {
		t.Fatalf("want PrimaryNotReady")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func parseInt(s string) int64 {
	var n int64
	for _, ch := range s {
		n = n*10 + int64(ch-'0')
	}
	return n
}

var _ = strings.Compare
