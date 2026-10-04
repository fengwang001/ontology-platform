package alloc_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/alloc"
	"ontology/node"
)

// op 是随机序列中的一次操作。
type op struct {
	kind            int // 0 addNode 1 other 2 exclude 3 createIndex 4 reroute 5 explain
	id, zone, index string
	num, num2       int
	size, other     int64
	on              bool
}

func (o op) String() string {
	switch o.kind {
	case 0:
		return fmt.Sprintf("AddNode(%s,%s,%d)", o.id, o.zone, o.size)
	case 1:
		return fmt.Sprintf("SetOther(%s,%d)", o.id, o.other)
	case 2:
		return fmt.Sprintf("SetExclude(%s,%v)", o.id, o.on)
	case 3:
		return fmt.Sprintf("CreateIndex(%s,S=%d,r=%d,size=%d)", o.index, o.num, o.num2, o.size)
	case 4:
		return "Reroute()"
	default:
		return fmt.Sprintf("Explain(%s,%d,primary=%v)", o.index, o.num, o.on)
	}
}

// TestRandomVsNaive 用 1500 组随机操作序列对照生产实现与逐步朴素模拟。
func TestRandomVsNaive(t *testing.T) {
	for seed := int64(0); seed < 1500; seed++ {
		t.Run(fmt.Sprintf("seed%04d", seed), func(t *testing.T) {
			runOneRandom(t, rand.New(rand.NewSource(seed)), seed)
		})
	}
}

func runOneRandom(t *testing.T, rng *rand.Rand, seed int64) {
	t.Helper()
	L := 1 + rng.Intn(100)
	H := L + rng.Intn(101-L)
	c, err := node.New(L, H)
	if err != nil {
		t.Fatal(err)
	}
	m := newSim(L, H)
	a := alloc.New(c)

	var seq []op
	var log []string
	var nodeIDs []string
	var indexNames []string
	nNodes := 2 + rng.Intn(6) // 2..7 个节点

	failf := func(format string, args ...any) {
		t.Fatalf("seed=%d L=%d H=%d\n%s\n---- sequence ----\n%s\n---- basis ----\n%s",
			seed, L, H, fmt.Sprintf(format, args...),
			opsString(seq), strings.Join(log, "\n"))
	}

	apply := func(o op) {
		seq = append(seq, o)
		switch o.kind {
		case 0:
			e1 := c.AddNode(o.id, o.zone, o.size)
			if e1 != nil {
				failf("AddNode %s: %v", o.id, e1)
			}
			m.addNode(o.id, o.zone, o.size)
			nodeIDs = append(nodeIDs, o.id)
		case 1:
			if e := c.SetOther(o.id, o.other); e != nil {
				failf("SetOther: %v", e)
			}
			m.setOther(o.id, o.other)
		case 2:
			if e := c.SetExclude(o.id, o.on); e != nil {
				failf("SetExclude: %v", e)
			}
			m.setExclude(o.id, o.on)
		case 3:
			if e := c.CreateIndex(o.index, o.num, o.num2, o.size); e != nil {
				failf("CreateIndex: %v", e)
			}
			m.createIndex(o.index, o.num, o.num2, o.size)
			indexNames = append(indexNames, o.index)
		case 4:
			res, e := a.Reroute()
			if e != nil {
				failf("Reroute: %v", e)
			}
			neededWork := m.needsWork()
			m.log = nil
			sa, sm := m.reroute()
			if itemsKey(res.Assigned) != itemsKey(sa) || itemsKey(res.Moved) != itemsKey(sm) {
				failf("Reroute mismatch\n real A=%v\n sim  A=%v\n real M=%v\n sim  M=%v",
					res.Assigned, sa, res.Moved, sm)
			}
			log = append(log, fmt.Sprintf("Reroute -> A=%v M=%v", res.Assigned, res.Moved))
			log = append(log, m.log...)

			// evals：本轮开始前无事可做时必须 0；否则不超过 尝试份数×节点数×4。
			if !neededWork {
				if alloc.Evals() != 0 {
					failf("evals=%d but sim reports no work needed", alloc.Evals())
				}
			} else {
				upper := m.trials * nNodes * 4
				if alloc.Evals() > upper {
					failf("evals=%d exceeds trials(%d)*nodes(%d)*4=%d",
						alloc.Evals(), m.trials, nNodes, upper)
				}
			}
		case 5:
			got, e := a.Explain(o.index, o.num, o.on)
			if e != nil {
				failf("Explain: %v", e)
			}
			want, notReady, ok := m.explain(o.index, o.num, o.on)
			if !ok || len(got.Verdicts) != len(want) {
				failf("Explain count mismatch")
			}
			for i := range want {
				if got.Verdicts[i].Node != want[i].Node ||
					got.Verdicts[i].Rule != want[i].Rule ||
					got.Verdicts[i].Pass != want[i].Pass {
					failf("Explain %s/%d primary=%v node=%s real=%+v sim=%+v",
						o.index, o.num, o.on, want[i].Node, got.Verdicts[i], want[i])
				}
			}
			if got.PrimaryNotReady != notReady {
				failf("Explain PrimaryNotReady real=%v sim=%v",
					got.PrimaryNotReady, notReady)
			}
		}
	}

	zones := []string{"z1", "z2", "z3"}
	for i := 0; i < nNodes; i++ {
		id := fmt.Sprintf("n%02d", i)
		total := int64(50 + rng.Intn(950)) // 50..999
		apply(op{kind: 0, id: id, zone: zones[rng.Intn(len(zones))], size: total})
		// 预置非分片占用：含 0 与超过 total 的情形
		if rng.Intn(3) == 0 {
			apply(op{kind: 1, id: id, other: int64(rng.Intn(int(total) + 100))})
		}
		if rng.Intn(6) == 0 {
			apply(op{kind: 2, id: id, on: true})
		}
	}

	nIdx := 1 + rng.Intn(4) // 1..4 个索引
	for i := 0; i < nIdx; i++ {
		name := fmt.Sprintf("idx%d", i)
		o := op{
			kind: 3, index: name,
			num:  1 + rng.Intn(4), // S 1..4
			num2: rng.Intn(6),     // r 0..5
			size: int64(1 + rng.Intn(120)),
		}
		apply(o)
	}

	// 交错执行：Reroute、改水位、改排除、Explain
	rounds := 4 + rng.Intn(5)
	for k := 0; k < rounds; k++ {
		apply(op{kind: 4})
		nid := nodeIDs[rng.Intn(len(nodeIDs))]
		switch rng.Intn(3) {
		case 0:
			n := m.nodes[nid]
			apply(op{kind: 1, id: nid, other: int64(rng.Intn(int(n.total) + 120))})
		case 1:
			apply(op{kind: 2, id: nid, on: rng.Intn(2) == 0})
		case 2:
			if len(indexNames) > 0 {
				name := indexNames[rng.Intn(len(indexNames))]
				ix := m.idx[name]
				apply(op{kind: 5, index: name, num: rng.Intn(ix.s),
					on: ix.r == 0 || rng.Intn(2) == 0}) // r=0 只能问主
			}
		}
	}
	apply(op{kind: 4})
}

func opsString(seq []op) string {
	var b strings.Builder
	for i, o := range seq {
		fmt.Fprintf(&b, "%d: %s\n", i, o)
	}
	return b.String()
}
