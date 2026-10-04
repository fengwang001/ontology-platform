package alloc

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/node"
)

type opKind int

const (
	opAddNode opKind = iota
	opSetOther
	opSetExclude
	opCreateIndex
	opReroute
	opExplain
)

type op struct {
	kind               opKind
	id, zone, index    string
	total, other, size int64
	on                 bool
	s, r, shard        int
	primary            bool
}

func (o op) String() string {
	switch o.kind {
	case opAddNode:
		return fmt.Sprintf("AddNode(%s,%s,%d)", o.id, o.zone, o.total)
	case opSetOther:
		return fmt.Sprintf("SetOther(%s,%d)", o.id, o.other)
	case opSetExclude:
		return fmt.Sprintf("SetExclude(%s,%v)", o.id, o.on)
	case opCreateIndex:
		return fmt.Sprintf("CreateIndex(%s,S=%d,r=%d,size=%d)", o.index, o.s, o.r, o.size)
	case opReroute:
		return "Reroute()"
	default:
		return fmt.Sprintf("Explain(%s,%d,primary=%v)", o.index, o.shard, o.primary)
	}
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, node.ErrInvalidArg):
		return "invalid"
	case errors.Is(err, node.ErrAlreadyExists):
		return "exists"
	case errors.Is(err, node.ErrNotFound):
		return "notfound"
	case errors.Is(err, node.ErrShardMissing):
		return "shardmissing"
	default:
		return "other"
	}
}

func placementFromCluster(cl *node.Cluster) map[string]string {
	out := map[string]string{}
	for _, nv := range cl.NodesLocked() {
		for _, cp := range nv.Copies {
			out[placementKey(cp.Index, cp.Shard, cp.Primary, cp.Replica)] = nv.ID
		}
	}
	return out
}

const poolSize = 8

func nodeName(i int) string  { return fmt.Sprintf("n%02d", i) }
func zoneName(i int) string  { return fmt.Sprintf("z%d", i) }
func indexName(i int) string { return fmt.Sprintf("idx%02d", i) }

func genOps(rng *rand.Rand) []op {
	n := 20 + rng.Intn(60)
	ops := make([]op, 0, n)
	var maxS int
	for len(ops) < n {
		switch rng.Intn(6) {
		case 0:
			id := nodeName(rng.Intn(poolSize))
			zone := zoneName(rng.Intn(1 + rng.Intn(4)))
			ops = append(ops, op{kind: opAddNode, id: id, zone: zone,
				total: int64(50 + rng.Intn(200))})
		case 1:
			ops = append(ops, op{kind: opSetOther, id: nodeName(rng.Intn(poolSize)),
				other: int64(rng.Intn(260))})
		case 2:
			ops = append(ops, op{kind: opSetExclude, id: nodeName(rng.Intn(poolSize)),
				on: rng.Intn(2) == 0})
		case 3:
			idx := indexName(rng.Intn(6))
			s := 1 + rng.Intn(4)
			if rng.Intn(20) == 0 { // occasionally invalid
				s = 65
			}
			if sh := s; sh > maxS {
				maxS = sh
			}
			ops = append(ops, op{kind: opCreateIndex, index: idx, s: s,
				r: rng.Intn(5), size: int64(1 + rng.Intn(80))})
		case 4:
			ops = append(ops, op{kind: opReroute})
		default:
			ops = append(ops, op{kind: opExplain, index: indexName(rng.Intn(7)),
				shard: rng.Intn(8), primary: rng.Intn(2) == 0})
		}
	}
	return ops
}

func runOneSeq(t *testing.T, seed int64, verbose bool) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	l, h := 1+rng.Intn(30), 60+rng.Intn(41)
	if l > h {
		l, h = h, l
	}
	cl, err := node.NewCluster(l, h)
	if err != nil {
		t.Fatal(err)
	}
	m := newNaive(l, h)
	ops := genOps(rng)

	if verbose {
		t.Logf("seed=%d L=%d H=%d ops=%d", seed, l, h, len(ops))
	}
	for step, o := range ops {
		if verbose {
			t.Logf("step=%d INPUT %s", step, o)
		}
		var gotErr, wantErr error
		var gotRes, wantRes Result
		var gotEvals uint64
		var gotVerdicts, wantVerdicts []Verdict

		switch o.kind {
		case opAddNode:
			gotErr = cl.AddNode(o.id, o.zone, o.total)
			wantErr = m.addNode(o.id, o.zone, o.total)
		case opSetOther:
			gotErr = cl.SetOther(o.id, o.other)
			wantErr = m.setOther(o.id, o.other)
		case opSetExclude:
			gotErr = cl.SetExclude(o.id, o.on)
			wantErr = m.setExclude(o.id, o.on)
		case opCreateIndex:
			gotErr = cl.CreateIndex(o.index, o.s, o.r, o.size)
			wantErr = m.createIndex(o.index, o.s, o.r, o.size)
		case opReroute:
			cl.Lock()
			gotRes = rerouteLocked(cl, &gotEvals)
			cl.Unlock()
			wantRes = m.reroute()
		case opExplain:
			gotVerdicts, gotErr = Explain(cl, o.index, o.shard, o.primary)
			wantVerdicts, wantErr = m.explain(o.index, o.shard, o.primary)
		}

		if errClass(gotErr) != errClass(wantErr) {
			t.Fatalf("seed=%d step=%d op=%s error got=%v want=%v", seed, step, o, gotErr, wantErr)
		}
		if o.kind == opReroute {
			if movementsString(gotRes.Assigned) != movementsString(wantRes.Assigned) ||
				movementsString(gotRes.Moved) != movementsString(wantRes.Moved) {
				t.Fatalf("seed=%d step=%d %s result mismatch:\n got A=[%s] M=[%s]\nwant A=[%s] M=[%s]\n%s",
					seed, step, o,
					movementsString(gotRes.Assigned), movementsString(gotRes.Moved),
					movementsString(wantRes.Assigned), movementsString(wantRes.Moved),
					dumpOps(ops[:step+1]))
			}
			// Eval bound: tried copies (unassigned + migration candidates) is at
			// most total defined copies; bound <= copies*nodes*4.
			totalCopies := 0
			for _, ix := range cl.IndexesLocked() {
				totalCopies += ix.S * (1 + ix.R)
			}
			bound := uint64(totalCopies * len(cl.NodesLocked()) * 4)
			if gotEvals > bound {
				t.Fatalf("seed=%d evals=%d > bound=%d", seed, gotEvals, bound)
			}
			assertInvariants(t, cl, seed, step)
			if verbose {
				t.Logf("step=%d %s A=[%s] M=[%s] evals=%d",
					step, o, movementsString(gotRes.Assigned), movementsString(gotRes.Moved), gotEvals)
				for _, line := range m.log {
					t.Logf("    %s", line)
				}
			}
			m.log = nil
		}
		if o.kind == opExplain && gotErr == nil {
			if len(gotVerdicts) != len(wantVerdicts) {
				t.Fatalf("seed=%d explain length %d != %d", seed, len(gotVerdicts), len(wantVerdicts))
			}
			for i := range gotVerdicts {
				if gotVerdicts[i] != wantVerdicts[i] {
					t.Fatalf("seed=%d step=%d %s verdict %d got=%+v want=%+v",
						seed, step, o, i, gotVerdicts[i], wantVerdicts[i])
				}
			}
		}
		if o.kind != opReroute {
			gp, wp := placementFromCluster(cl), m.placement()
			if fmt.Sprint(gp) != fmt.Sprint(wp) {
				t.Fatalf("seed=%d step=%d %s placement drift\ngot=%v\nwant=%v\n%s",
					seed, step, o, gp, wp, dumpOps(ops[:step+1]))
			}
		}
	}
	// Final full-state comparison.
	gp, wp := placementFromCluster(cl), m.placement()
	if fmt.Sprint(gp) != fmt.Sprint(wp) {
		t.Fatalf("seed=%d final placement mismatch\ngot=%v\nwant=%v", seed, gp, wp)
	}
}

func dumpOps(ops []op) string {
	s := "history:\n"
	for i, o := range ops {
		s += fmt.Sprintf("  %d: %s\n", i, o)
	}
	return s
}

func TestRandomDifferential1500(t *testing.T) {
	for i := 0; i < 1500; i++ {
		seed := int64(1000 + i)
		runOneSeq(t, seed, i < 5)
		if i > 0 && i%300 == 0 {
			t.Logf("differential progress: %d/1500 sequences match naive simulator", i)
		}
	}
}

func assertInvariants(t *testing.T, cl *node.Cluster, seed int64, step int) {
	t.Helper()
	for _, nv := range cl.NodesLocked() {
		shards := map[string]bool{}
		for _, cp := range nv.Copies {
			key := fmt.Sprintf("%s/%d", cp.Index, cp.Shard)
			if shards[key] {
				t.Fatalf("seed=%d step=%d node %s hosts two copies of %s", seed, step, nv.ID, key)
			}
			shards[key] = true
		}
	}
}
