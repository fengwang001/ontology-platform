package collab

import (
	"strconv"
	"testing"
)

// genOps builds consecutive ops starting at start. groupIDOffset avoids group
// id collisions when a replay prefix is concatenated with new ops.
func (w *diffWorld) genOps(start int64, n, groupIDOffset int) []Op {
	ops := make([]Op, 0, n)
	groupRemaining := 0
	groupIdx := groupIDOffset
	groupID := ""
	depends := false
	for i := 0; i < n; i++ {
		if groupRemaining == 0 {
			groupIdx++
			groupRemaining = 1 + w.rng.Intn(3)
			groupID = "g" + strconv.Itoa(groupIdx)
			depends = groupIdx > 1 && w.rng.Intn(3) == 0
		}
		groupRemaining--
		field := w.fields[w.rng.Intn(len(w.fields))]
		op := Op{Seq: start + int64(i), Field: field, Group: groupID, Depends: depends}
		if w.kinds[field] == KindSet {
			op.Type = OpSet
			op.Value = int64(w.rng.Intn(21)) - 10
			op.BaseVer = w.pickBaseVer(field)
		} else {
			op.Type = OpAdd
			op.Delta = int64(w.rng.Intn(7)) - 3
			if op.Delta == 0 {
				op.Delta = 1
			}
		}
		ops = append(ops, op)
	}
	return ops
}

func (w *diffWorld) pickBaseVer(field string) int64 {
	cur := w.naive.docs["d"].fields[field].version
	switch w.rng.Intn(10) {
	case 0, 1, 2:
		return cur
	case 3, 4:
		return 0
	case 5:
		if cur > 0 {
			return cur - 1
		}
		return cur
	case 6:
		return cur + 1 + int64(w.rng.Intn(3))
	default:
		return int64(w.rng.Intn(4))
	}
}

// malform mutates a well-formed batch into an invalid-parameter shape.
func (w *diffWorld) malform(ops []Op) {
	switch w.rng.Intn(7) {
	case 0:
		ops[w.rng.Intn(len(ops))].Group = ""
	case 1:
		ops[w.rng.Intn(len(ops))].Field = "ghost"
	case 2:
		ops[w.rng.Intn(len(ops))].Type = 99
	case 3:
		if len(ops) >= 3 {
			ops[0].Group, ops[2].Group = "z", "z" // non-adjacent
		} else {
			ops[0].Group = ""
		}
	case 4:
		// duplicate Set within one group
		setField := ""
		for _, f := range w.fields {
			if w.kinds[f] == KindSet {
				setField = f
				break
			}
		}
		if setField == "" || len(ops) < 2 {
			ops[0].Group = ""
			break
		}
		g := ops[0].Group
		ops[0] = Op{Seq: ops[0].Seq, Type: OpSet, Field: setField, Value: 1, BaseVer: 0, Group: g}
		ops[1] = Op{Seq: ops[1].Seq, Type: OpSet, Field: setField, Value: 2, BaseVer: 0, Group: g}
	case 5:
		// first group depends
		ops[0].Depends = true
	case 6:
		// non-consecutive seqs inside batch
		if len(ops) >= 2 {
			ops[len(ops)-1].Seq += 1
		} else {
			ops[0].Group = ""
		}
	}
}

func (w *diffWorld) runStep(t *testing.T, step int) {
	t.Helper()
	client := "c" + strconv.Itoa(w.rng.Intn(3))
	maxSeq := w.pend[client]

	var ops []Op
	now := w.now
	mode := w.rng.Intn(100)
	switch {
	case mode < 55:
		ops = w.genOps(maxSeq+1, 1+w.rng.Intn(6), 0)
		now = w.now + int64(w.rng.Intn(3))
	case mode < 70 && maxSeq > 0:
		// pure replay of a retained prefix
		lowest := maxSeq - RetainWindow + 1
		if lowest < 1 {
			lowest = 1
		}
		start := lowest + w.rng.Int63n(maxSeq-lowest+1)
		n := 1 + w.rng.Intn(4)
		if start+int64(n)-1 > maxSeq {
			n = int(maxSeq-start) + 1
		}
		ops = w.replayOps(client, start, n)
		now = w.now + int64(w.rng.Intn(2))
	case mode < 82 && maxSeq > 0:
		// mixed replay prefix + new suffix, keeping group ids disjoint
		r := 1 + w.rng.Intn(3)
		if int64(r) > maxSeq {
			r = int(maxSeq)
		}
		start := maxSeq - int64(r) + 1
		rep := w.replayOps(client, start, r)
		// replay ops keep their original group ids; new suffix must use
		// distinct ids to preserve adjacency
		news := w.genOps(maxSeq+1, 1+w.rng.Intn(5), 100000+int(maxSeq))
		ops = append(ops, rep...)
		ops = append(ops, news...)
		now = w.now + int64(w.rng.Intn(2))
	case mode < 88:
		ops = w.genOps(maxSeq+1, 1+w.rng.Intn(6), 0)
		if now > 0 {
			now = w.rng.Int63n(now + 1)
		}
	case mode < 93 && maxSeq > RetainWindow+2:
		ops = w.genOps(maxSeq-RetainWindow-1, 1, 0)
		now = w.now + 1
	case mode < 98 && maxSeq > 0:
		ops = w.genOps(maxSeq+2+int64(w.rng.Intn(3)), 1+w.rng.Intn(4), 0)
		now = w.now + 1
	default:
		ops = w.genOps(maxSeq+1, 1+w.rng.Intn(6), 0)
		w.malform(ops)
		now = w.now + 1
	}

	w.log.printf("step=%d", step)
	w.logBatch(client, ops, now)

	rres, rerr := w.real.Sync(client, "d", ops, now)
	nres, nclass := w.naive.sync(client, ops, now)
	rc := rejectClass(rerr)

	if rc != nclass {
		for _, cc := range []string{"c0", "c1", "c2"} {
			w.log.printf("diag pending %s real=%d naive=%d", cc,
				w.real.Pending(cc, "d"), w.naive.clients[[2]string{cc, "d"}].maxSeq)
		}
		w.log.printf("MISMATCH reject real=%q(%v) naive=%q", rc, rerr, nclass)
		w.dump(t)
		t.FailNow()
	}
	if rc == "" {
		if len(rres.Results) != len(nres) {
			w.log.printf("MISMATCH length %d vs %d", len(rres.Results), len(nres))
			w.dump(t)
			t.FailNow()
		}
		for i := range nres {
			if rres.Results[i] != nres[i] {
				for _, cc := range []string{"c0", "c1", "c2"} {
					w.log.printf("diag pending %s real=%d naive=%d", cc,
						w.real.Pending(cc, "d"), w.naive.clients[[2]string{cc, "d"}].maxSeq)
				}
				w.log.printf("MISMATCH idx=%d real=%+v naive=%+v",
					i, rres.Results[i], nres[i])
				w.dump(t)
				t.FailNow()
			}
		}
		w.log.printf("  -> %s", formatKinds(rres.Results))
		w.now = now
		w.recordAccepted(client, ops, maxSeq)
		if last := ops[len(ops)-1].Seq; last > w.pend[client] {
			w.pend[client] = last
		}
	} else {
		w.log.printf("  -> REJECTED %s (%v)", rc, rerr)
	}
	w.check(t)
}

func groupCount(ops []Op) int {
	n := 0
	for i := range ops {
		if i == 0 || ops[i].Group != ops[i-1].Group {
			n++
		}
	}
	return n
}

// replayOps returns verbatim copies of previously accepted ops [start, start+n).
func (w *diffWorld) replayOps(client string, start int64, n int) []Op {
	h := w.hist[client]
	out := make([]Op, 0, n)
	for seq := start; seq < start+int64(n); seq++ {
		op, ok := h[seq]
		if !ok {
			panic("replayOps: missing history for seq " + strconv.FormatInt(seq, 10))
		}
		out = append(out, op)
	}
	return out
}

// recordAccepted stores ops with seq above the prior maxSeq verbatim.
func (w *diffWorld) recordAccepted(client string, ops []Op, priorMax int64) {
	h := w.hist[client]
	if h == nil {
		h = map[int64]Op{}
		w.hist[client] = h
	}
	for _, op := range ops {
		if op.Seq > priorMax {
			h[op.Seq] = op
		}
	}
}

func TestRandomDifferential(t *testing.T) {
	trials := 2000
	if v := osGetenvInt("DIFF_TRIALS"); v > 0 {
		trials = v
	}
	for trial := 0; trial < trials; trial++ {
		w := newDiffWorld(int64(trial*1000003 + 17))
		steps := 20 + w.rng.Intn(40)
		for step := 0; step < steps; step++ {
			w.runStep(t, step)
		}
	}
	t.Logf("differential test finished: %d trials", trials)
}
