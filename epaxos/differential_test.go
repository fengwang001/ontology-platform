package epaxos

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// This file cross-checks Scheduler against a naive reference implementation
// that follows the spec literally: it computes reachability closures to find
// blocked instances and equivalence classes (mutually reachable = one SCC),
// then emits ready classes smallest-first-member first.

type mrec struct {
	seq      int
	deps     []Instance
	executed bool
}

type commitOp struct {
	inst Instance
	seq  int
	deps []Instance
}

func lessModel(recs map[Instance]*mrec, a, b Instance) bool {
	ra, rb := recs[a], recs[b]
	if ra.seq != rb.seq {
		return ra.seq < rb.seq
	}
	if a.R != b.R {
		return a.R < b.R
	}
	return a.I < b.I
}

// naiveExecute runs one execution round on the model and returns the order.
// It also reports the blocked set and the SCC partition for logging.
func naiveExecute(recs map[Instance]*mrec) (out, blocked []Instance, classes [][]Instance) {
	var nodes []Instance
	for inst, r := range recs {
		if !r.executed {
			nodes = append(nodes, inst)
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].R != nodes[j].R {
			return nodes[i].R < nodes[j].R
		}
		return nodes[i].I < nodes[j].I
	})

	// Edges only point at committed-but-unexecuted deps.
	edges := make(map[Instance][]Instance)
	for _, u := range nodes {
		for _, d := range recs[u].deps {
			if dr, ok := recs[d]; ok && !dr.executed {
				edges[u] = append(edges[u], d)
			}
		}
	}

	// Naive reachability closure per node.
	reach := make(map[Instance]map[Instance]bool)
	for _, u := range nodes {
		seen := make(map[Instance]bool)
		var dfs func(x Instance)
		dfs = func(x Instance) {
			for _, w := range edges[x] {
				if !seen[w] {
					seen[w] = true
					dfs(w)
				}
			}
		}
		dfs(u)
		reach[u] = seen
	}

	// u is executable iff u and everything reachable from u have all deps
	// committed.
	isExec := make(map[Instance]bool)
	for _, u := range nodes {
		ok := true
		check := func(x Instance) {
			for _, d := range recs[x].deps {
				if _, committed := recs[d]; !committed {
					ok = false
				}
			}
		}
		check(u)
		for v := range reach[u] {
			check(v)
		}
		isExec[u] = ok
	}
	var execNodes []Instance
	for _, u := range nodes {
		if isExec[u] {
			execNodes = append(execNodes, u)
		} else {
			blocked = append(blocked, u)
		}
	}

	// Equivalence classes by mutual reachability.
	classOf := make(map[Instance]int)
	for _, u := range execNodes {
		if _, ok := classOf[u]; ok {
			continue
		}
		var members []Instance
		for _, v := range execNodes {
			if u == v || (reach[u][v] && reach[v][u]) {
				members = append(members, v)
			}
		}
		id := len(classes)
		for _, m := range members {
			classOf[m] = id
		}
		classes = append(classes, members)
	}
	for _, members := range classes {
		sort.Slice(members, func(i, j int) bool {
			return lessModel(recs, members[i], members[j])
		})
	}

	// Emit ready classes, smallest first member first.
	emitted := make([]bool, len(classes))
	for done := 0; done < len(classes); {
		best := -1
		for cid, members := range classes {
			if emitted[cid] {
				continue
			}
			ready := true
			for _, m := range members {
				for _, d := range edges[m] {
					if cd := classOf[d]; cd != cid && !emitted[cd] {
						ready = false
					}
				}
			}
			if ready && (best == -1 || lessModel(recs, classes[cid][0], classes[best][0])) {
				best = cid
			}
		}
		for _, m := range classes[best] {
			out = append(out, m)
			recs[m].executed = true
		}
		emitted[best] = true
		done++
	}
	return out, blocked, classes
}

func naivePending(recs map[Instance]*mrec) []Instance {
	var out []Instance
	for inst, r := range recs {
		if !r.executed {
			out = append(out, inst)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].R != out[j].R {
			return out[i].R < out[j].R
		}
		return out[i].I < out[j].I
	})
	return out
}

func formatOps(recs map[Instance]*mrec, ops []commitOp) string {
	var b strings.Builder
	for _, op := range ops {
		fmt.Fprintf(&b, "  %v seq=%d deps=%v\n", op.inst, op.seq, op.deps)
	}
	return b.String()
}

func applyCommits(t *testing.T, s *Scheduler, ops []commitOp) {
	t.Helper()
	for _, op := range ops {
		if err := s.Commit(op.inst, op.seq, op.deps); err != nil {
			t.Fatalf("Commit(%v, %d, %v) failed: %v", op.inst, op.seq, op.deps, err)
		}
	}
}

func TestExecuteMatchesNaiveReference(t *testing.T) {
	const seeds = 300
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(4)
		numInst := 3 + rng.Intn(8)
		if maxInst := n * 6; numInst > maxInst {
			numInst = maxInst
		}

		// Unique random instances.
		seen := make(map[Instance]bool)
		var insts []Instance
		for len(insts) < numInst {
			inst := Instance{rng.Intn(n), 1 + rng.Intn(6)}
			if !seen[inst] {
				seen[inst] = true
				insts = append(insts, inst)
			}
		}
		// Phantom instances are referenced as deps but committed only in
		// round 2, exercising the blocking rules.
		var phantoms []commitOp
		numPhantoms := rng.Intn(3)
		for k := 0; k < numPhantoms; k++ {
			ph := Instance{rng.Intn(n), 100 + k}
			phantoms = append(phantoms, commitOp{ph, 1 + rng.Intn(8), nil})
		}

		var ops []commitOp
		for _, inst := range insts {
			var deps []Instance
			for _, other := range insts {
				if other != inst && rng.Float64() < 0.3 {
					deps = append(deps, other)
				}
			}
			if numPhantoms > 0 && rng.Float64() < 0.35 {
				deps = append(deps, phantoms[rng.Intn(numPhantoms)].inst)
			}
			ops = append(ops, commitOp{inst, 1 + rng.Intn(8), deps})
		}

		cap := numInst + numPhantoms + 5
		schedA, _ := New(n, cap)
		schedB, _ := New(n, cap) // same set, different commit order
		recs := make(map[Instance]*mrec)

		orderA := append([]commitOp(nil), ops...)
		rng.Shuffle(len(orderA), func(i, j int) { orderA[i], orderA[j] = orderA[j], orderA[i] })
		orderB := append([]commitOp(nil), ops...)
		rng.Shuffle(len(orderB), func(i, j int) { orderB[i], orderB[j] = orderB[j], orderB[i] })

		applyCommits(t, schedA, orderA)
		applyCommits(t, schedB, orderB)
		for _, op := range ops {
			recs[op.inst] = &mrec{seq: op.seq, deps: op.deps}
		}

		for round := 0; round < 2; round++ {
			if round == 1 {
				applyCommits(t, schedA, phantoms)
				applyCommits(t, schedB, phantoms)
				for _, op := range phantoms {
					recs[op.inst] = &mrec{seq: op.seq, deps: op.deps}
				}
			}
			gotA := schedA.Execute()
			gotB := schedB.Execute()
			want, blocked, classes := naiveExecute(recs)

			t.Logf("seed=%d round=%d N=%d input:\n%sblocked=%v components=%v",
				seed, round, n, formatOps(recs, ops), blocked, classes)
			t.Logf("seed=%d round=%d output=%v", seed, round, want)

			if !reflect.DeepEqual(append([]Instance(nil), gotA...), append([]Instance(nil), want...)) {
				t.Fatalf("seed=%d round=%d: scheduler A mismatch:\n got: %v\nwant: %v", seed, round, gotA, want)
			}
			if !reflect.DeepEqual(append([]Instance(nil), gotB...), append([]Instance(nil), want...)) {
				t.Fatalf("seed=%d round=%d: scheduler B (different commit order) mismatch:\n got: %v\nwant: %v", seed, round, gotB, want)
			}
			wantPending := naivePending(recs)
			if pendA := schedA.Pending(); !reflect.DeepEqual(append([]Instance(nil), pendA...), append([]Instance(nil), wantPending...)) {
				t.Fatalf("seed=%d round=%d: pending mismatch:\n got: %v\nwant: %v", seed, round, pendA, wantPending)
			}
		}
	}
}

// Replaying the same operation sequence reproduces the exact same execution
// sequence and errors.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n := 3
	var ops []commitOp
	seen := make(map[Instance]bool)
	for len(ops) < 12 {
		inst := Instance{rng.Intn(n), 1 + rng.Intn(5)}
		if seen[inst] {
			continue
		}
		seen[inst] = true
		var deps []Instance
		for other := range seen {
			if other != inst && rng.Float64() < 0.4 {
				deps = append(deps, other)
			}
		}
		ops = append(ops, commitOp{inst, 1 + rng.Intn(6), deps})
	}

	run := func() ([]Instance, []Instance, []error) {
		s, _ := New(n, 50)
		var errs []error
		for _, op := range ops {
			errs = append(errs, s.Commit(op.inst, op.seq, op.deps))
			// Duplicate and conflicting re-commits to exercise error paths.
			errs = append(errs, s.Commit(op.inst, op.seq, op.deps))
			errs = append(errs, s.Commit(op.inst, op.seq+1, op.deps))
		}
		exec := s.Execute()
		return exec, s.Pending(), errs
	}

	exec1, pend1, errs1 := run()
	exec2, pend2, errs2 := run()
	if !reflect.DeepEqual(exec1, exec2) {
		t.Fatalf("replay exec mismatch:\n%v\n%v", exec1, exec2)
	}
	if !reflect.DeepEqual(pend1, pend2) {
		t.Fatalf("replay pending mismatch:\n%v\n%v", pend1, pend2)
	}
	if len(errs1) != len(errs2) {
		t.Fatal("replay error count mismatch")
	}
	for i := range errs1 {
		var e1, e2 *Error
		ok1 := errors.As(errs1[i], &e1)
		ok2 := errors.As(errs2[i], &e2)
		if ok1 != ok2 || (ok1 && e1.Code != e2.Code) {
			t.Fatalf("replay error mismatch at %d: %v vs %v", i, errs1[i], errs2[i])
		}
	}
}
