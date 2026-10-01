package snapshotfs

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// recOp 记录一次调用的输入与输出，用于重放验证确定性。
type recOp struct {
	kind string
	iarg int64
	sarg string
	res  int64
	err  error
}

func replayOnLedger(t *testing.T, cap int64, rec []recOp) []recOp {
	t.Helper()
	l, err := New(cap)
	if err != nil {
		t.Fatalf("replay New: %v", err)
	}
	out := make([]recOp, 0, len(rec))
	for _, op := range rec {
		r := recOp{kind: op.kind, iarg: op.iarg, sarg: op.sarg}
		switch op.kind {
		case "alloc":
			r.res, r.err = l.Alloc(op.iarg)
		case "free":
			r.err = l.Free(op.iarg)
		case "snapshot":
			r.err = l.Snapshot(op.sarg)
		case "destroy":
			r.err = l.Destroy(op.sarg)
		case "hold":
			r.err = l.Hold(op.sarg)
		case "release":
			r.err = l.Release(op.sarg)
		case "rollback":
			r.err = l.Rollback(op.sarg)
		case "used":
			r.res = l.Used()
		case "cur":
			r.res = l.Cur()
		case "referenced":
			r.res, r.err = l.Referenced(op.sarg)
		case "unique":
			r.res, r.err = l.Unique(op.sarg)
		}
		out = append(out, r)
	}
	return out
}

func sameRec(a, b recOp) bool {
	return a.kind == b.kind && a.iarg == b.iarg && a.sarg == b.sarg &&
		a.res == b.res && sameErr(a.err, b.err)
}

// stateBasis 逐块打印判定依据（size/b/d/丢弃）与快照（事务号/持有计数）。
func stateBasis(m *model) string {
	s := fmt.Sprintf("cur=%d used=%d blocks{", m.cur, m.used())
	for id := int64(1); id <= m.maxID; id++ {
		bl := m.blks[id]
		d := "inf"
		if bl.d != infinity {
			d = fmt.Sprintf("%d", bl.d)
		}
		tag := ""
		if bl.discarded {
			tag = ",DISCARDED"
		}
		s += fmt.Sprintf("#%d[sz=%d,b=%d,d=%s%s] ", id, bl.size, bl.b, d, tag)
	}
	s += "} snaps{"
	for name, sp := range m.snaps {
		s += fmt.Sprintf("%s@t%d(h%d) ", name, sp.t, sp.hold)
	}
	return s + "}"
}

// checkInvariants 校验 Used<=Cap、各快照 Unique 之和 <= Used-现存字节、
// Ledger 与模型在所有查询上一致。
func checkInvariants(t *testing.T, seq, step int, l *Ledger, m *model, rec []recOp) {
	t.Helper()
	if got := l.Used(); got != m.used() {
		t.Fatalf("seq %d step %d used: led %d model %d\n%s", seq, step, got, m.used(), stateBasis(m))
	}
	if got := l.Cur(); got != m.cur {
		t.Fatalf("seq %d step %d cur: led %d model %d", seq, step, got, m.cur)
	}
	if m.used() > m.cap {
		t.Fatalf("seq %d step %d used %d > cap %d", seq, step, m.used(), m.cap)
	}
	var sumUnique int64
	for name := range m.snaps {
		rv, err := l.Referenced(name)
		if err != nil || rv != m.referenced(name) {
			t.Fatalf("seq %d Referenced(%s): led (%d,%v) model %d", seq, name, rv, err, m.referenced(name))
		}
		uv, err := l.Unique(name)
		if err != nil || uv != m.unique(name) {
			t.Fatalf("seq %d Unique(%s): led (%d,%v) model %d", seq, name, uv, err, m.unique(name))
		}
		sumUnique += uv
	}
	if sumUnique > m.used()-m.aliveBytes() {
		t.Fatalf("seq %d sumUnique %d > used-alive %d\n%s",
			seq, sumUnique, m.used()-m.aliveBytes(), stateBasis(m))
	}
}

// TestRandomVsModel 2000 组随机操作序列对照朴素模型：每步打印输入、输出
// 与判定依据，步后校验不变量，序列结束后重放验证结果完全一致。
func TestRandomVsModel(t *testing.T) {
	const sequences = 2000
	const length = 18
	const cap = int64(300)
	names := []string{"a", "b", "s"}

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewPCG(uint64(seq+1)*2654435761, uint64(sequences-seq)))
		md := newModel(cap)
		l, err := New(cap)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		rec := make([]recOp, 0, length)
		t.Logf("seq %d START %s", seq, stateBasis(md))

		for step := 0; step < length; step++ {
			op := recOp{}
			r := rng.IntN(100)
			switch {
			case r < 24:
				op.kind = "alloc"
				if rng.IntN(12) == 0 {
					op.iarg = int64(rng.IntN(3)) - 1
				} else {
					op.iarg = int64(1 + rng.IntN(90))
				}
				mres, merr := md.alloc(op.iarg)
				op.res, op.err = l.Alloc(op.iarg)
				if !sameErr(op.err, merr) || op.res != mres {
					t.Fatalf("seq %d alloc(%d): led (%d,%v) model (%d,%v)", seq, op.iarg, op.res, op.err, mres, merr)
				}
			case r < 40:
				op.kind = "free"
				if rng.IntN(8) == 0 {
					op.iarg = int64(rng.IntN(2))
				} else {
					op.iarg = int64(1 + rng.IntN(int(md.maxID)+3))
				}
				merr := md.free(op.iarg)
				op.err = l.Free(op.iarg)
				if !sameErr(op.err, merr) {
					t.Fatalf("seq %d free(%d): led %v model %v", seq, op.iarg, op.err, merr)
				}
			case r < 52:
				op.kind = "snapshot"
				if rng.IntN(10) == 0 {
					op.sarg = ""
				} else {
					op.sarg = names[rng.IntN(len(names))]
				}
				merr := md.snapshot(op.sarg)
				op.err = l.Snapshot(op.sarg)
				if !sameErr(op.err, merr) {
					t.Fatalf("seq %d snapshot(%q): led %v model %v", seq, op.sarg, op.err, merr)
				}
			case r < 60:
				op.kind = "destroy"
				op.sarg = names[rng.IntN(len(names))]
				op.err = l.Destroy(op.sarg)
				if !sameErr(op.err, md.destroy(op.sarg)) {
					t.Fatalf("seq %d destroy(%q) mismatch", seq, op.sarg)
				}
			case r < 66:
				op.kind = "hold"
				op.sarg = names[rng.IntN(len(names))]
				op.err = l.Hold(op.sarg)
				if !sameErr(op.err, md.hold(op.sarg)) {
					t.Fatalf("seq %d hold(%q) mismatch", seq, op.sarg)
				}
			case r < 72:
				op.kind = "release"
				op.sarg = names[rng.IntN(len(names))]
				op.err = l.Release(op.sarg)
				if !sameErr(op.err, md.release(op.sarg)) {
					t.Fatalf("seq %d release(%q) mismatch", seq, op.sarg)
				}
			case r < 80:
				op.kind = "rollback"
				op.sarg = names[rng.IntN(len(names))]
				op.err = l.Rollback(op.sarg)
				if !sameErr(op.err, md.rollback(op.sarg)) {
					t.Fatalf("seq %d rollback(%q) mismatch", seq, op.sarg)
				}
			case r < 88:
				op.kind = "used"
				op.res = l.Used()
			case r < 92:
				op.kind = "cur"
				op.res = l.Cur()
			case r < 96:
				op.kind = "referenced"
				op.sarg = names[rng.IntN(len(names))]
				op.res, op.err = l.Referenced(op.sarg)
				if _, ok := md.snaps[op.sarg]; ok {
					if op.err != nil || op.res != md.referenced(op.sarg) {
						t.Fatalf("seq %d referenced(%q) mismatch", seq, op.sarg)
					}
				} else if !sameErr(op.err, ErrSnapshotMissing) {
					t.Fatalf("seq %d referenced(%q): %v", seq, op.sarg, op.err)
				}
			default:
				op.kind = "unique"
				op.sarg = names[rng.IntN(len(names))]
				op.res, op.err = l.Unique(op.sarg)
				if _, ok := md.snaps[op.sarg]; ok {
					if op.err != nil || op.res != md.unique(op.sarg) {
						t.Fatalf("seq %d unique(%q) mismatch", seq, op.sarg)
					}
				} else if !sameErr(op.err, ErrSnapshotMissing) {
					t.Fatalf("seq %d unique(%q): %v", seq, op.sarg, op.err)
				}
			}

			t.Logf("seq %d step %2d IN  %s iarg=%d sarg=%q", seq, step, op.kind, op.iarg, op.sarg)
			t.Logf("seq %d step %2d OUT res=%d err=%s | %s",
				seq, step, op.res, errName(op.err), stateBasis(md))
			rec = append(rec, op)
			checkInvariants(t, seq, step, l, md, rec)
		}

		replayed := replayOnLedger(t, cap, rec)
		for i := range rec {
			if !sameRec(rec[i], replayed[i]) {
				t.Fatalf("seq %d replay mismatch at step %d: %+v vs %+v\n%s",
					seq, i, rec[i], replayed[i], stateBasis(md))
			}
		}
		t.Logf("seq %d DONE replay identical (%d ops)", seq, len(rec))
	}
}
