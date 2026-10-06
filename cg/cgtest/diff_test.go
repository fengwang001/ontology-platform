package cgtest_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/cg"
	"ontology/cg/cgtest"
)

// Event types in the shared random operation language.
const (
	evCreate = iota
	evAdd
	evRemove
	evBegin
	evConfirm
	evCommit
	evAbort
	evWrite
	evQVolume
	evQGroup
	evQLast
	evCount
)

type event struct {
	typ      int
	t        int64
	gid      string
	vid      string
	vids     []string
	data     string
	deadline int64
}

const poolSize = 8

func volName(i int) string { return fmt.Sprintf("v%d", i) }

// generator produces random, well-formed (though often rejected) events.
type generator struct {
	r     *rand.Rand
	lastT int64
}

func (g *generator) advance() int64 {
	// Time either stays equal (same-instant ordering matters) or jumps
	// forward by 0..4 ticks.
	g.lastT += int64(g.r.Intn(5))
	return g.lastT
}

func (g *generator) pickGroup() string {
	return fmt.Sprintf("g%d", g.r.Intn(3)+1)
}

func (g *generator) pickVol() string {
	return volName(g.r.Intn(poolSize))
}

func (g *generator) next(writeN int) event {
	t := g.advance()
	typ := g.r.Intn(evCount)
	e := event{typ: typ, t: t, gid: g.pickGroup(), vid: g.pickVol(), data: fmt.Sprintf("w%d", writeN)}
	switch typ {
	case evCreate:
		n := 2 + g.r.Intn(3)
		used := map[int]bool{}
		for len(e.vids) < n {
			i := g.r.Intn(poolSize)
			if !used[i] {
				used[i] = true
				e.vids = append(e.vids, volName(i))
			}
		}
	case evAdd:
		e.vids = []string{volName(g.r.Intn(poolSize))}
	case evRemove:
		e.vids = []string{volName(g.r.Intn(poolSize))}
	case evBegin:
		e.deadline = t + int64(g.r.Intn(4)) // t+0..3 (equal legal)
	case evConfirm:
	case evWrite:
	}
	return e
}

// cmpState compares full observable state after every event.
func cmpState(t *testing.T, c *cg.Coordinator, n *cgtest.Naive, now int64) {
	t.Helper()
	nseqs, nqueued := n.SnapshotSeqs(), n.SnapshotQueued()
	for i := 0; i < poolSize; i++ {
		vid := volName(i)
		st, errC := c.GetVolume(now, vid)
		_, okN := nseqs[vid]
		if okN {
			if errC != nil {
				t.Fatalf("GetVolume(%s): %v", vid, errC)
			}
			if st.Seq != nseqs[vid] || st.QueuedWrites != nqueued[vid] {
				t.Fatalf("volume %s diverged: real(seq=%d queued=%d) model(seq=%d queued=%d)",
					vid, st.Seq, st.QueuedWrites, nseqs[vid], nqueued[vid])
			}
		} else {
			if errC == nil {
				t.Fatalf("volume %s should not exist, real reports %+v", vid, st)
			}
		}
	}
	for gi := 1; gi <= 3; gi++ {
		gid := fmt.Sprintf("g%d", gi)
		gst, errC := c.GetGroup(now, gid)
		nerr2 := n.ReadGroup(now, gid)
		if nerr2 != nil {
			if cg.Kind(nerr2.Kind) != cg.KindOf(errC) {
				t.Fatalf("group %s read rejection: real=%v model=%d", gid, errC, nerr2.Kind)
			}
			continue
		}
		if errC != nil {
			t.Fatalf("GetGroup(%s): %v", gid, errC)
		}
		np := n.Phase(gid)
		wantPhase := map[int]cg.Phase{0: cg.Idle, 1: cg.Freezing, 2: cg.Frozen}[np]
		if gst.Phase != wantPhase {
			t.Fatalf("group %s phase: real=%s(%d) model=%d members=%v confirmed=%v deadline=%d point=%d",
				gid, gst.Phase, gst.Phase, np, gst.Members, gst.Confirmed, gst.Deadline, gst.Point)
		}
		rid, rpt, rcut, rok, nerr2 := n.LastSnapshot(now, gid)
		rec, recErr := c.LastSnapshot(now, gid)
		realExist := cg.KindOf(recErr)
		if nerr2 != nil {
			if cg.Kind(nerr2.Kind) != realExist {
				t.Fatalf("group %s last-snapshot rejection diverged: real=%d model=%d", gid, realExist, nerr2.Kind)
			}
			continue
		}
		if realExist != cg.OK {
			t.Fatalf("group %s last-snapshot read rejected: %v", gid, recErr)
		}
		if rok != (rec != nil) {
			t.Fatalf("group %s last-snapshot existence diverged real=%v model=%v", gid, rec, rok)
		}
		if rok {
			if rec.ID != rid || rec.Point != rpt {
				t.Fatalf("group %s record header diverged: real=%+v model=(%d,%d)", gid, rec, rid, rpt)
			}
			if len(rec.Cutoffs) != len(rcut) {
				t.Fatalf("group %s cutoff length diverged", gid)
			}
			for k, v := range rcut {
				if rec.Cutoffs[k] != v {
					t.Fatalf("group %s cutoff %s real=%d model=%d", gid, k, rec.Cutoffs[k], v)
				}
			}
		}
	}
}

func kindToNaive(k cg.Kind) cgtest.Kind { return cgtest.Kind(k) }

func runOne(t *testing.T, c *cg.Coordinator, n *cgtest.Naive, e event, writeN int) {
	t.Helper()
	var realKind, modelKind cg.Kind = cg.OK, cg.OK
	var detail string
	switch e.typ {
	case evCreate:
		err := c.CreateGroup(e.t, cg.GroupSpec{GroupID: e.gid, VolumeIDs: e.vids})
		nerr := n.CreateGroup(e.t, e.gid, e.vids)
		if err != nil {
			realKind = cg.KindOf(err)
		}
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
	case evAdd:
		err := c.AddMembers(e.t, e.gid, e.vids)
		nerr := n.AddMembers(e.t, e.gid, e.vids)
		if err != nil {
			realKind = cg.KindOf(err)
		}
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
	case evRemove:
		err := c.RemoveMembers(e.t, e.gid, e.vids)
		nerr := n.RemoveMembers(e.t, e.gid, e.vids)
		if err != nil {
			realKind = cg.KindOf(err)
		}
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
	case evBegin:
		_, err := c.BeginSnapshot(e.t, e.gid, e.deadline)
		_, nerr := n.BeginSnapshot(e.t, e.gid, e.deadline)
		if err != nil {
			realKind = cg.KindOf(err)
		}
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
	case evConfirm:
		_, err := c.ConfirmFreeze(e.t, e.gid, e.vid)
		_, nerr := n.ConfirmFreeze(e.t, e.gid, e.vid)
		if err != nil {
			realKind = cg.KindOf(err)
		}
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
	case evCommit:
		_, err := c.Commit(e.t, e.gid)
		_, _, _, nerr := n.Commit(e.t, e.gid)
		if err != nil {
			realKind = cg.KindOf(err)
		}
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
	case evAbort:
		err := c.Abort(e.t, e.gid)
		nerr := n.Abort(e.t, e.gid)
		if err != nil {
			realKind = cg.KindOf(err)
		}
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
	case evWrite:
		r, err := c.Write(e.t, e.vid, e.data)
		nr, nerr := n.Write(e.t, e.vid, e.data)
		if err != nil {
			realKind = cg.KindOf(err)
		}
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
		if err == nil && (r.Seq != nr.Seq || r.Queued != nr.Queued) {
			t.Fatalf("write result diverged: real=%+v model=%+v", r, nr)
		}
	case evQVolume:
		_, err := c.GetVolume(e.t, e.vid)
		nerr := n.ReadVolume(e.t, e.vid)
		realKind = cg.KindOf(err)
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
	case evQGroup:
		_, err := c.GetGroup(e.t, e.gid)
		nerr := n.ReadGroup(e.t, e.gid)
		realKind = cg.KindOf(err)
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
	case evQLast:
		rec, err := c.LastSnapshot(e.t, e.gid)
		_, _, _, nok, nerr := n.LastSnapshot(e.t, e.gid)
		if err != nil {
			realKind = cg.KindOf(err)
		}
		if nerr != nil {
			modelKind = cg.Kind(nerr.Kind)
		}
		if (err == nil && rec != nil) != nok {
			t.Fatalf("last snapshot existence diverged")
		}
	}
	if realKind != modelKind {
		t.Fatalf("event %+v rejection diverged: real=%s model=%s (%s)", e, realKind, modelKind, detail)
	}
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("differential fuzzing skipped in -short mode")
	}
	for seed := int64(0); seed < 120; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			c := cg.New(cg.Config{DefaultQueueCapacity: 1 + r.Intn(3), MaxFreezeHold: 1 + r.Int63n(4)})
			c.WithTracer(cg.NewLogTracer(testLogWriter{t}))
			n := cgtest.NewNaive(1+int(r.Intn(3)), 1+int64(r.Intn(4)))
			gen := &generator{r: r}
			for i := 0; i < 240; i++ {
				e := gen.next(i)
				t.Logf("[seed=%d #%d] %s", seed, i, formatEvent(e))
				runOne(t, c, n, e, i)
				cmpState(t, c, n, gen.lastT)
			}
		})
	}
}

func formatEvent(e event) string {
	switch e.typ {
	case evCreate:
		return fmt.Sprintf("CREATE g=%s vols=%v t=%d", e.gid, e.vids, e.t)
	case evAdd:
		return fmt.Sprintf("ADD g=%s vols=%v t=%d", e.gid, e.vids, e.t)
	case evRemove:
		return fmt.Sprintf("REMOVE g=%s vols=%v t=%d", e.gid, e.vids, e.t)
	case evBegin:
		return fmt.Sprintf("BEGIN g=%s deadline=%d t=%d", e.gid, e.deadline, e.t)
	case evConfirm:
		return fmt.Sprintf("CONFIRM g=%s v=%s t=%d", e.gid, e.vid, e.t)
	case evCommit:
		return fmt.Sprintf("COMMIT g=%s t=%d", e.gid, e.t)
	case evAbort:
		return fmt.Sprintf("ABORT g=%s t=%d", e.gid, e.t)
	case evWrite:
		return fmt.Sprintf("WRITE v=%s data=%s t=%d", e.vid, e.data, e.t)
	case evQVolume:
		return fmt.Sprintf("QVOL v=%s t=%d", e.vid, e.t)
	case evQGroup:
		return fmt.Sprintf("QGRP g=%s t=%d", e.gid, e.t)
	default:
		return fmt.Sprintf("QLAST g=%s t=%d", e.gid, e.t)
	}
}

// testLogWriter routes tracer lines into t.Log so `go test -v` prints the
// per-operation input/output/reason log.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}
