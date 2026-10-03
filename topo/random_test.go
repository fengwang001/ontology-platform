package topo

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	var ab, bb *BatchError
	aBatch, bBatch := errors.As(a, &ab), errors.As(b, &bb)
	if aBatch || bBatch {
		if !aBatch || !bBatch {
			return false
		}
		return ab.Index == bb.Index && sameErr(ab.Reason, bb.Reason)
	}
	var ac, bc *CycleError
	aCycle, bCycle := errors.As(a, &ac), errors.As(b, &bc)
	if aCycle || bCycle {
		if !aCycle || !bCycle {
			return false
		}
		return eqInts(ac.Path, bc.Path)
	}
	return errors.Is(a, b)
}

// checkInvariants verifies the topological validity of the maintainer and
// its exact agreement with the model after one operation.
func checkInvariants(t *testing.T, m *Maintainer, mo *model) {
	t.Helper()
	if len(m.alive) != len(mo.alive) {
		t.Fatalf("alive count %d != model %d", len(m.alive), len(mo.alive))
	}
	seenOrd := make(map[int]int, len(m.alive))
	for x := range m.alive {
		o, ok := m.ord[x]
		if !ok {
			t.Fatalf("alive node %d has no order value", x)
		}
		if prev, dup := seenOrd[o]; dup {
			t.Fatalf("order value %d shared by nodes %d and %d", o, prev, x)
		}
		seenOrd[o] = x
		if !mo.alive[x] {
			t.Fatalf("node %d alive in maintainer but not in model", x)
		}
		if mo.ord[x] != o {
			t.Fatalf("ord(%d) = %d, model says %d", x, o, mo.ord[x])
		}
	}
	edgeCnt := 0
	for u, ws := range m.out {
		for w := range ws {
			edgeCnt++
			if !m.in[w][u] {
				t.Fatalf("edge %d->%d missing from in-adjacency", u, w)
			}
			if !mo.out[u][w] {
				t.Fatalf("edge %d->%d missing in model", u, w)
			}
			if m.ord[u] >= m.ord[w] {
				t.Fatalf("edge %d->%d violates order: %d !< %d", u, w, m.ord[u], m.ord[w])
			}
		}
	}
	if edgeCnt != m.edgeCount {
		t.Fatalf("edgeCount = %d, actual %d", m.edgeCount, edgeCnt)
	}
	if edgeCnt != mo.nedge {
		t.Fatalf("maintainer has %d edges, model has %d", edgeCnt, mo.nedge)
	}
	if got, want := m.Order(), mo.order(); !eqInts(got, want) {
		t.Fatalf("Order = %v, model says %v", got, want)
	}
	if got, want := m.Touched(), mo.touch; got != want {
		t.Fatalf("touched = %d, model says %d", got, want)
	}
}

// TestRandomAgainstModel replays 2000 random operation sequences (including
// batches) on two independent Maintainer instances and on the naive model,
// requiring identical results, identical witnesses and valid topology after
// every single operation. Every operation is logged with its input, output
// and decision basis (visible with go test -v).
func TestRandomAgainstModel(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		n := 3 + rng.Intn(38)
		e := 1 + rng.Intn(4*n)
		m1 := New(n, e)
		m2 := New(n, e)
		mo := newModel(n, e)
		steps := 60 + rng.Intn(80)
		log := func(format string, args ...interface{}) {
			t.Logf("seq=%d "+format, append([]interface{}{seq}, args...)...)
		}
		pickEndpoint := func() int {
			alive := make([]int, 0, len(mo.alive))
			for x := range mo.alive {
				alive = append(alive, x)
			}
			r := rng.Intn(100)
			switch {
			case r < 75 && len(alive) > 0:
				return alive[rng.Intn(len(alive))]
			case r < 95 && mo.nextID > 0:
				return rng.Intn(mo.nextID)
			default:
				return rng.Intn(mo.nextID + 2)
			}
		}
		for step := 0; step < steps; step++ {
			choice := rng.Intn(100)
			switch {
			case choice < 12 && mo.nextID < mo.N:
				id1, err1 := m1.AddNode()
				id2, err2 := m2.AddNode()
				idM, errM := mo.addNode()
				log("step=%d op=AddNode -> %d,%v (limit=%d created=%d)", step, idM, errM, mo.N, mo.nextID)
				if id1 != idM || id2 != idM || !sameErr(err1, errM) || !sameErr(err2, errM) {
					t.Fatalf("seq=%d step=%d AddNode: (%d,%v) vs model (%d,%v)", seq, step, id1, err1, idM, errM)
				}
			case choice < 45:
				u, v := pickEndpoint(), pickEndpoint()
				if rng.Intn(20) == 0 {
					v = u // self loop
				}
				ou, ov := mo.ord[u], mo.ord[v]
				r1, err1 := m1.AddEdge(u, v)
				r2, err2 := m2.AddEdge(u, v)
				rM, errM := mo.addEdge(u, v)
				log("step=%d op=AddEdge(%d,%d) ord=(%d,%d) -> moved=%v counts=(%d,%d) err=%v",
					step, u, v, ou, ov, rM.Moved, rM.DeltaF, rM.DeltaB, errM)
				if !sameErr(err1, errM) || !sameErr(err2, errM) {
					t.Fatalf("seq=%d step=%d AddEdge(%d,%d): err %v / %v vs model %v", seq, step, u, v, err1, err2, errM)
				}
				if errM == nil {
					if !eqInts(r1.Moved, rM.Moved) || !eqInts(r2.Moved, rM.Moved) {
						t.Fatalf("seq=%d step=%d AddEdge(%d,%d): moved %v / %v vs model %v", seq, step, u, v, r1.Moved, r2.Moved, rM.Moved)
					}
					if r1.DeltaF != rM.DeltaF || r1.DeltaB != rM.DeltaB || r2.DeltaF != rM.DeltaF || r2.DeltaB != rM.DeltaB {
						t.Fatalf("seq=%d step=%d AddEdge(%d,%d): counts differ from model", seq, step, u, v)
					}
				}
			case choice < 58:
				var u, v int
				if rng.Intn(2) == 0 && mo.nedge > 0 {
					k := rng.Intn(mo.nedge)
					for a, ws := range mo.out {
						for b := range ws {
							if k == 0 {
								u, v = a, b
							}
							k--
						}
					}
				} else {
					u, v = pickEndpoint(), pickEndpoint()
				}
				err1 := m1.RemoveEdge(u, v)
				err2 := m2.RemoveEdge(u, v)
				errM := mo.removeEdge(u, v)
				log("step=%d op=RemoveEdge(%d,%d) -> %v", step, u, v, errM)
				if !sameErr(err1, errM) || !sameErr(err2, errM) {
					t.Fatalf("seq=%d step=%d RemoveEdge(%d,%d): %v / %v vs model %v", seq, step, u, v, err1, err2, errM)
				}
			case choice < 68:
				x := pickEndpoint()
				err1 := m1.RemoveNode(x)
				err2 := m2.RemoveNode(x)
				errM := mo.removeNode(x)
				log("step=%d op=RemoveNode(%d) -> %v", step, x, errM)
				if !sameErr(err1, errM) || !sameErr(err2, errM) {
					t.Fatalf("seq=%d step=%d RemoveNode(%d): %v / %v vs model %v", seq, step, x, err1, err2, errM)
				}
			case choice < 83:
				size := 1 + rng.Intn(8)
				if rng.Intn(40) == 0 {
					size = 0 // illegal size
				}
				batch := make([][2]int, size)
				for i := range batch {
					batch[i] = [2]int{pickEndpoint(), pickEndpoint()}
				}
				r1, err1 := m1.AddEdges(batch)
				r2, err2 := m2.AddEdges(batch)
				rM, errM := mo.addEdges(batch)
				log("step=%d op=AddEdges(%v) -> moved=%v counts=%v err=%v", step, batch, rM.Moved, rM.Counts, errM)
				if !sameErr(err1, errM) || !sameErr(err2, errM) {
					t.Fatalf("seq=%d step=%d AddEdges(%v): %v / %v vs model %v", seq, step, batch, err1, err2, errM)
				}
				if errM == nil {
					if !eqInts(r1.Moved, rM.Moved) || !eqInts(r2.Moved, rM.Moved) {
						t.Fatalf("seq=%d step=%d AddEdges: moved %v / %v vs model %v", seq, step, r1.Moved, r2.Moved, rM.Moved)
					}
					if !reflect.DeepEqual(r1.Counts, rM.Counts) || !reflect.DeepEqual(r2.Counts, rM.Counts) {
						t.Fatalf("seq=%d step=%d AddEdges: counts differ from model", seq, step)
					}
				}
			default:
				o1, o2, oM := m1.Order(), m2.Order(), mo.order()
				log("step=%d op=Order -> %v", step, oM)
				if !eqInts(o1, oM) || !eqInts(o2, oM) {
					t.Fatalf("seq=%d step=%d Order: %v / %v vs model %v", seq, step, o1, o2, oM)
				}
				x := pickEndpoint()
				v1, e1 := m1.OrdOf(x)
				v2, e2 := m2.OrdOf(x)
				vM, eM := mo.ord[x], error(nil)
				if !mo.alive[x] {
					eM = ErrNodeNotFound
				}
				log("step=%d op=OrdOf(%d) -> %d,%v", step, x, vM, eM)
				if !sameErr(e1, eM) || !sameErr(e2, eM) || (eM == nil && (v1 != vM || v2 != vM)) {
					t.Fatalf("seq=%d step=%d OrdOf(%d): (%d,%v) vs model (%d,%v)", seq, step, x, v1, e1, vM, eM)
				}
			}
			checkInvariants(t, m1, mo)
			checkInvariants(t, m2, mo)
		}
	}
}

// TestDeterministicReplay runs one long random sequence twice and requires
// bit-identical orders and witnesses.
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	type op struct {
		kind  string
		u, v  int
		batch [][2]int
	}
	var ops []op
	for i := 0; i < 500; i++ {
		switch rng.Intn(5) {
		case 0:
			ops = append(ops, op{kind: "node"})
		case 1:
			ops = append(ops, op{kind: "edge", u: rng.Intn(60), v: rng.Intn(60)})
		case 2:
			ops = append(ops, op{kind: "unedge", u: rng.Intn(60), v: rng.Intn(60)})
		case 3:
			ops = append(ops, op{kind: "unnode", u: rng.Intn(60)})
		case 4:
			b := make([][2]int, 1+rng.Intn(6))
			for j := range b {
				b[j] = [2]int{rng.Intn(60), rng.Intn(60)}
			}
			ops = append(ops, op{kind: "batch", batch: b})
		}
	}
	run := func() ([]string, []int) {
		m := New(100, 5000)
		var trace []string
		for _, o := range ops {
			switch o.kind {
			case "node":
				id, err := m.AddNode()
				trace = append(trace, fmt.Sprintf("node %d %v", id, err))
			case "edge":
				r, err := m.AddEdge(o.u, o.v)
				trace = append(trace, fmt.Sprintf("edge %d %d %v %v", o.u, o.v, r, err))
			case "unedge":
				trace = append(trace, fmt.Sprintf("unedge %d %d %v", o.u, o.v, m.RemoveEdge(o.u, o.v)))
			case "unnode":
				trace = append(trace, fmt.Sprintf("unnode %d %v", o.u, m.RemoveNode(o.u)))
			case "batch":
				r, err := m.AddEdges(o.batch)
				trace = append(trace, fmt.Sprintf("batch %v %v", r, err))
			}
		}
		return trace, m.Order()
	}
	trace1, order1 := run()
	trace2, order2 := run()
	if !reflect.DeepEqual(trace1, trace2) {
		t.Fatal("replay produced a different trace")
	}
	if !eqInts(order1, order2) {
		t.Fatal("replay produced a different order")
	}
}
