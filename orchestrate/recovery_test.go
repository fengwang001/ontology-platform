package orchestrate

import (
	"errors"
	"testing"

	"ontology/graph"
	"ontology/journal"
	"ontology/step"
)

func TestCrashPointsNoRerunAndSameOutcome(t *testing.T) {
	g := chainABC()

	bc := newCounters()
	bo, _ := New(g, Config{}, actionsFor(g, bc))
	base, err := bo.Run()
	if err != nil {
		t.Fatal(err)
	}
	baseExec := map[string]int{}
	for _, sv := range base.Steps {
		baseExec[sv.ID] = sv.ExecCount
	}
	if base.Phase != PhaseCompleted || base.JournalLen != 6 {
		t.Fatalf("baseline wrong: phase=%s len=%d", base.Phase, base.JournalLen)
	}

	points := []journal.CrashPoint{journal.BeforeWrite, journal.MidWrite, journal.AfterWrite}
	for seq := 1; seq <= base.JournalLen; seq++ {
		for _, pt := range points {
			seq, pt := seq, pt
			t.Run(pt.String()+"/"+itoa(seq), func(t *testing.T) {
				runSuccessCrash(t, g, seq, pt, baseExec, PhaseCompleted)
			})
		}
	}
}

func TestCrashPointsDuringFailureAndCompensation(t *testing.T) {
	g := chainABC()
	bc := newCounters()
	bc.failExec["C"] = 1
	bo, _ := New(g, Config{}, actionsFor(g, bc))
	base, err := bo.Run()
	if err != nil {
		t.Fatal(err)
	}
	if base.Phase != PhaseAborted {
		t.Fatalf("baseline phase %s", base.Phase)
	}
	baseExec, baseComp := map[string]int{}, map[string]int{}
	for _, sv := range base.Steps {
		baseExec[sv.ID] = sv.ExecCount
		baseComp[sv.ID] = sv.CompCount
	}
	points := []journal.CrashPoint{journal.BeforeWrite, journal.MidWrite, journal.AfterWrite}
	for seq := 1; seq <= base.JournalLen; seq++ {
		for _, pt := range points {
			seq, pt := seq, pt
			t.Run(pt.String()+"/"+itoa(seq), func(t *testing.T) {
				runFailCrash(t, g, seq, pt, baseExec, baseComp)
			})
		}
	}
}

func runSuccessCrash(t *testing.T, g *graph.Graph, seq int, pt journal.CrashPoint, baseExec map[string]int, want Phase) {
	t.Helper()
	c := newCounters()
	o, _ := New(g, Config{}, actionsFor(g, c))
	if err := o.SetHook(CrashHookAt(CrashTarget{Seq: seq, Point: pt})); err != nil {
		t.Fatal(err)
	}
	_, runErr := o.Run()
	var ce *CrashedError
	if !errors.As(runErr, &ce) {
		t.Fatalf("want CrashedError, got %v", runErr)
	}
	assertRecoverEqual(t, g, o.JournalImage(), baseExec, nil, want)
}

func runFailCrash(t *testing.T, g *graph.Graph, seq int, pt journal.CrashPoint, baseExec, baseComp map[string]int) {
	t.Helper()
	c := newCounters()
	c.failExec["C"] = 1
	o, _ := New(g, Config{}, actionsFor(g, c))
	if err := o.SetHook(CrashHookAt(CrashTarget{Seq: seq, Point: pt})); err != nil {
		t.Fatal(err)
	}
	_, runErr := o.Run()
	var ce *CrashedError
	if !errors.As(runErr, &ce) {
		t.Fatalf("want CrashedError, got %v", runErr)
	}
	assertRecoverEqual(t, g, o.JournalImage(), baseExec, baseComp, PhaseAborted)
}

func assertRecoverEqual(t *testing.T, g *graph.Graph, img []byte, baseExec, baseComp map[string]int, want Phase) {
	t.Helper()
	c := newCounters()
	if baseComp != nil {
		c.failExec["C"] = 1
	}
	o2, snap0, err := Recover(g, Config{}, actionsFor(g, c), img, nil)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	// Second replay from the identical image must produce an equal snapshot.
	cDup := newCounters()
	if baseComp != nil {
		cDup.failExec["C"] = 1
	}
	_, snapDup, err := Recover(g, Config{}, actionsFor(g, cDup), img, nil)
	if err != nil || !snapshotsEqual(snap0, snapDup) {
		t.Fatalf("replay not idempotent: %v", err)
	}
	final, err := o2.Run()
	if err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if final.Phase != want {
		t.Fatalf("final phase %s want %s", final.Phase, want)
	}
	for _, sv := range final.Steps {
		postE, postC := c.counts(sv.ID)
		// The durable attempt count (start records) must equal the baseline:
		// re-driving an in-flight attempt adds NO new start record.
		if sv.ExecCount != baseExec[sv.ID] {
			t.Fatalf("%s durable exec count %d, base %d (pre=%d func-reruns=%d)",
				sv.ID, sv.ExecCount, baseExec[sv.ID], snapCount(snap0, sv.ID), postE)
		}
		if baseComp != nil {
			if sv.CompCount != baseComp[sv.ID] {
				t.Fatalf("%s durable comp count %d, base %d (pre=%d reruns=%d)",
					sv.ID, sv.CompCount, baseComp[sv.ID], snapComp(snap0, sv.ID), postC)
			}
		}
		if snapState(snap0, sv.ID) == step.Done && postE != 0 {
			t.Fatalf("%s re-executed after being done (%d)", sv.ID, postE)
		}
		if snapState(snap0, sv.ID) == step.Compensated && postC != 0 {
			t.Fatalf("%s re-compensated after being compensated (%d)", sv.ID, postC)
		}
	}
}

func snapView(s Snapshot, id string) (StepView, bool) {
	for _, sv := range s.Steps {
		if sv.ID == id {
			return sv, true
		}
	}
	return StepView{}, false
}

func snapCount(s Snapshot, id string) int {
	if sv, ok := snapView(s, id); ok {
		return sv.ExecCount
	}
	return 0
}

func snapComp(s Snapshot, id string) int {
	if sv, ok := snapView(s, id); ok {
		return sv.CompCount
	}
	return 0
}

func snapState(s Snapshot, id string) step.State {
	if sv, ok := snapView(s, id); ok {
		return sv.State
	}
	return step.Pending
}

func snapshotsEqual(a, b Snapshot) bool {
	if a.Phase != b.Phase || a.JournalLen != b.JournalLen || len(a.Steps) != len(b.Steps) {
		return false
	}
	for i := range a.Steps {
		x, y := a.Steps[i], b.Steps[i]
		if x != y {
			return false
		}
	}
	return true
}
