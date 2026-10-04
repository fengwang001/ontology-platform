package sched

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/slot"
)

// dOp is one generated operation. target 0 means "pick a known accepted id".
type dOp struct {
	kind             int // 0 submit, 1 finish, 2 ackcancel, 3 cancel
	group            string
	cancel, prot, ok bool
	target           int64
}

func genOps(rng *rand.Rand, groups []string, n int) []dOp {
	ops := make([]dOp, n)
	for i := range ops {
		if rng.Intn(10) < 5 {
			ops[i] = dOp{
				kind:   0,
				group:  groups[rng.Intn(len(groups))],
				cancel: rng.Intn(3) == 0,
				prot:   rng.Intn(4) == 0,
			}
			continue
		}
		// Targeted ops: target resolved lazily in runDifferential.
		ops[i] = dOp{kind: 1 + rng.Intn(3), ok: rng.Intn(2) == 0}
	}
	return ops
}

// runDifferential replays one op stream on both implementations and compares
// error class, accepted id, every run's state, held-slot count and queue order
// after each step. It returns a decision log for failure diagnostics.
func runDifferential(t *testing.T, C, Q int, ops []dOp, rng *rand.Rand) []string {
	t.Helper()
	s := mustNew(t, C, Q)
	o := newOracle(C, Q)
	log := []string{fmt.Sprintf("SEQUENCE C=%d Q=%d ops=%d", C, Q, len(ops))}
	var accepted []int64

	for i, op := range ops {
		var sErr, oErr error
		var sID, oID int64
		var basis string

		if op.kind == 0 {
			sID, sErr = s.Submit([]byte(op.group), op.cancel, op.prot)
			st := o.submit(op.group, op.cancel, op.prot)
			oErr, oID, basis = st.err, st.id, st.basis
			if sErr == nil {
				accepted = append(accepted, sID)
			}
			log = append(log, fmt.Sprintf(
				"step %4d INPUT Submit(group=%q,cancel=%v,protected=%v) OUTPUT id=%d err=%s | ORACLE id=%d : %s",
				i, op.group, op.cancel, op.prot, sID, classOf(sErr), oID, basis))
			if !sameClass(sErr, oErr) {
				t.Fatalf("step %d submit err: sched=%s oracle=%s\n%s", i, classOf(sErr), classOf(oErr), joinLog(log))
			}
			if oErr == nil && sID != oID {
				t.Fatalf("step %d submit id: sched=%d oracle=%d\n%s", i, sID, oID, joinLog(log))
			}
		} else {
			if len(accepted) == 0 { // nothing to target: turn it into a submit
				op = dOp{kind: 0, group: "g"}
				sID, sErr = s.Submit([]byte(op.group), false, false)
				st := o.submit(op.group, false, false)
				accepted = append(accepted, sID)
				log = append(log, fmt.Sprintf("step %4d (retargeted) Submit -> id=%d err=%s | %s",
					i, sID, classOf(sErr), st.basis))
				if !sameClass(sErr, st.err) {
					t.Fatalf("step %d retarget: %s vs %s", i, classOf(sErr), classOf(st.err))
				}
			} else {
				id := accepted[rng.Intn(len(accepted))]
				var st oStep
				switch op.kind {
				case 1:
					sErr = s.Finish(id, op.ok)
					st = o.finish(id, op.ok)
				case 2:
					sErr = s.AckCancel(id)
					st = o.ack(id)
				case 3:
					sErr = s.Cancel(id)
					st = o.cancel(id)
				}
				oErr, basis = st.err, st.basis
				name := []string{"submit", "Finish", "AckCancel", "Cancel"}[op.kind]
				log = append(log, fmt.Sprintf(
					"step %4d INPUT %s(id=%d,ok=%v) OUTPUT err=%s | ORACLE %s : %s",
					i, name, id, op.ok, classOf(sErr), classOf(oErr), basis))
				if !sameClass(sErr, oErr) {
					t.Fatalf("step %d %s id=%d: sched=%s oracle=%s\n%s",
						i, name, id, classOf(sErr), classOf(oErr), joinLog(log))
				}
			}
		}

		// Structural comparison after every step.
		if s.slots.Held() != o.held {
			t.Fatalf("step %d held: sched=%d oracle=%d\n%s", i, s.slots.Held(), o.held, joinLog(log))
		}
		if s.slots.Queued() != len(o.queue) {
			t.Fatalf("step %d qlen: sched=%d oracle=%d\n%s", i, s.slots.Queued(), len(o.queue), joinLog(log))
		}
		// Queue order: walk the list front-to-back and compare.
		if elems := queueIDs(s); fmt.Sprint(elems) != fmt.Sprint(o.queue) {
			t.Fatalf("step %d queue order: sched=%v oracle=%v", i, elems, o.queue)
		}
		for id, ost := range o.state {
			gst, gerr := s.StateOf(id)
			if gerr != nil || gst != ost {
				t.Fatalf("step %d id=%d: sched=%s(%v) oracle=%s\n%s",
					i, id, gst, gerr, ost, joinLog(log))
			}
		}
		// Global invariants.
		if held := s.slots.Held(); held > s.slots.Capacity() {
			t.Fatalf("step %d: held %d > C", i, held)
		}
		if s.slots.Free() > 0 && s.slots.Queued() > 0 {
			t.Fatalf("step %d: free slot while queue non-empty", i)
		}
		for name, g := range o.grp {
			sg := s.groups.Lookup(name)
			if sg.Placeholder != g.placeholder || sg.Pending != g.pending {
				t.Fatalf("step %d group %q: sched=(%d,%d) oracle=(%d,%d)",
					i, name, sg.Placeholder, sg.Pending, g.placeholder, g.pending)
			}
		}
	}
	return log
}

func queueIDs(s *Scheduler) []int64 {
	var out []int64
	for e := s.slots.PeekList().Front(); e != nil; e = e.Next() {
		out = append(out, slot.ValueAt(e))
	}
	return out
}

func joinLog(log []string) string {
	tail := log
	if len(tail) > 40 {
		tail = tail[len(tail)-40:]
	}
	out := ""
	for _, l := range tail {
		out += l + "\n"
	}
	return out
}

func TestDifferential1500(t *testing.T) {
	const sequences = 1500
	var allLogs [][]string
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(1000 + seq)))
		C := 1 + rng.Intn(5)
		Q := rng.Intn(8)                           // small Q keeps rejection pressure high
		groups := []string{"", "g", "g", "h", "k"} // repeats make same-group churn likely
		ops := genOps(rng, groups, 40+rng.Intn(40))
		log := runDifferential(t, C, Q, ops, rng)
		if seq < 3 {
			allLogs = append(allLogs, log)
		}
	}
	for _, l := range allLogs {
		t.Logf("\n%s", joinLog(l))
	}
	t.Logf("differential sequences: %d all matched", sequences)
}

// TestDeterminismReplay replays one generated stream on two schedulers and
// checks identical outputs, satisfying "same sequence replays identically".
func TestDeterminismReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	groups := []string{"", "g", "h"}
	ops := genOps(rng, groups, 200)
	r1 := rand.New(rand.NewSource(7))
	r2 := rand.New(rand.NewSource(7))
	log1 := runDifferential(t, 2, 3, ops, r1)
	log2 := runDifferential(t, 2, 3, ops, r2)
	if fmt.Sprint(log1) != fmt.Sprint(log2) {
		t.Fatal("replay logs differ")
	}
}

// TestConcurrentInvariants hammers one scheduler concurrently; after joining,
// the held count, queue emptiness rule and per-state accounting must hold.
func TestConcurrentInvariants(t *testing.T) {
	s := mustNew(t, 4, 64)
	g := [][]byte{[]byte("g"), []byte("h"), nil}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			var mine []int64
			for i := 0; i < 300; i++ {
				if rng.Intn(2) == 0 || len(mine) == 0 {
					id, err := s.Submit(g[rng.Intn(3)], rng.Intn(3) == 0, rng.Intn(5) == 0)
					if err == nil {
						mine = append(mine, id)
					}
					continue
				}
				id := mine[rng.Intn(len(mine))]
				switch rng.Intn(3) {
				case 0:
					_ = s.Finish(id, rng.Intn(2) == 0)
				case 1:
					_ = s.AckCancel(id)
				case 2:
					_ = s.Cancel(id)
				}
			}
		}(int64(w + 1))
	}
	wg.Wait()
	if s.slots.Held() > s.slots.Capacity() {
		t.Fatalf("held %d > C", s.slots.Held())
	}
	if s.slots.Free() > 0 && s.slots.Queued() > 0 {
		t.Fatal("free slot but queue non-empty after quiescence")
	}
	var nonTerm, term int
	for _, r := range s.runs {
		if r.state.IsTerminal() {
			term++
		} else {
			nonTerm++
		}
	}
	if int64(term+nonTerm) != s.nextID-1 {
		t.Fatalf("submitted=%d terminal=%d nonterminal=%d", s.nextID-1, term, nonTerm)
	}
}
