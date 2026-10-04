package lot_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/lot"
	"ontology/plan"
)

func exampleTable(t *testing.T) *plan.Table {
	t.Helper()
	tab, err := plan.New([]plan.Range{
		{Lo: 8, Hi: 50, Normal: plan.Scheme{N: 13, Ac: 1, Re: 2}, Tightened: plan.Scheme{N: 20, Ac: 1, Re: 2}, Reduced: plan.Scheme{N: 5, Ac: 0, Re: 2}},
		{Lo: 51, Hi: 150, Normal: plan.Scheme{N: 13, Ac: 1, Re: 2}, Tightened: plan.Scheme{N: 20, Ac: 1, Re: 2}, Reduced: plan.Scheme{N: 5, Ac: 0, Re: 2}},
		{Lo: 151, Hi: 300, Normal: plan.Scheme{N: 32, Ac: 2, Re: 3}, Tightened: plan.Scheme{N: 50, Ac: 1, Re: 2}, Reduced: plan.Scheme{N: 8, Ac: 1, Re: 3}},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	return tab
}

var insp = lot.Operator{ID: "I1", Role: lot.RoleInspector}
var mgr = lot.Operator{ID: "M1", Role: lot.RoleManager}

func keyOf(i int) lot.StreamKey {
	return lot.StreamKey{Supplier: "S", Material: []string{"A", "B"}[i%2]}
}

func TestBoundaryAcRe(t *testing.T) {
	in := lot.NewInspector(exampleTable(t))
	k := keyOf(0)
	l1, err := in.Submit(insp, k, "L1", 60)
	if err != nil || l1.Scheme != (lot.Scheme{N: 13, Ac: 1, Re: 2}) {
		t.Fatalf("submit: %+v err=%v", l1, err)
	}
	d1, st1, err := in.Record(insp, "L1", 1)
	if err != nil || d1 != lot.Accept || st1 != lot.Released {
		t.Fatalf("d=Ac: %v %v %v", d1, st1, err)
	}
	in.Submit(insp, k, "L2", 60)
	_, st2, _ := in.Record(insp, "L2", 2)
	if st2 != lot.Rejected {
		t.Fatalf("d=Re must reject, got %v", st2)
	}
}

func TestExampleSwitchToTightened(t *testing.T) {
	in := lot.NewInspector(exampleTable(t))
	k := keyOf(0)
	ds := []int{0, 2, 1, 0, 0, 3}
	want := []lot.Status{lot.Released, lot.Rejected, lot.Released, lot.Released, lot.Released, lot.Rejected}
	for i, d := range ds {
		id := string(rune('A' + i))
		if _, err := in.Submit(insp, k, id, 60); err != nil {
			t.Fatal(err)
		}
		_, st, err := in.Record(insp, id, d)
		if err != nil || st != want[i] {
			t.Fatalf("lot %s: %v %v", id, st, err)
		}
	}
	sev, _ := in.Severity(k)
	if sev != plan.Tightened {
		t.Fatalf("want Tightened, got %v", sev)
	}
	l7, _ := in.Submit(insp, k, "G", 60)
	if l7.Scheme != (lot.Scheme{N: 20, Ac: 1, Re: 2}) {
		t.Fatalf("7th lot scheme: %+v", l7.Scheme)
	}
}

func TestSlidingWindow(t *testing.T) {
	in := lot.NewInspector(exampleTable(t))
	k := keyOf(0)
	ds := []int{0, 2, 1, 0, 0, 0, 2}
	for i, d := range ds {
		id := string(rune('A' + i))
		in.Submit(insp, k, id, 60)
		in.Record(insp, id, d)
	}
	if sev, _ := in.Severity(k); sev != plan.Normal {
		t.Fatalf("sliding window must not switch, got %v", sev)
	}
}

func TestReducedByLR(t *testing.T) {
	in := lot.NewInspector(exampleTable(t))
	k := keyOf(0)
	ds := []int{1, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0}
	for i, d := range ds {
		id := string(rune('A' + i))
		in.Submit(insp, k, id, 60)
		in.Record(insp, id, d)
	}
	sev, _ := in.Severity(k)
	if sev != plan.Reduced {
		t.Fatalf("want Reduced, got %v", sev)
	}
	lr, _ := in.Submit(insp, k, "LR", 60)
	if lr.Scheme != (lot.Scheme{N: 5, Ac: 0, Re: 2}) {
		t.Fatalf("reduced scheme: %+v", lr.Scheme)
	}
	dec, st, _ := in.Record(insp, "LR", 1)
	if dec != lot.BorderlineAccept || st != lot.Released {
		t.Fatalf("borderline: %v %v", dec, st)
	}
	if sev2, _ := in.Severity(k); sev2 != plan.Normal {
		t.Fatalf("borderline must go Normal, got %v", sev2)
	}
}

func TestSuspendAndResume(t *testing.T) {
	in := lot.NewInspector(exampleTable(t))
	k := keyOf(0)
	dmap := map[string]int{"B": 2, "F": 2}
	for _, id := range []string{"A", "B", "C", "D", "E", "F"} {
		in.Submit(insp, k, id, 60)
		in.Record(insp, id, dmap[id])
	}
	if sev, _ := in.Severity(k); sev != plan.Tightened {
		t.Fatalf("setup want Tightened, got %v", sev)
	}
	ids := []string{"T1", "T2", "T3", "T4", "T5", "T6", "T7", "T8", "T9"}
	for i, id := range ids {
		in.Submit(insp, k, id, 60)
		_, st, err := in.Record(insp, id, 2-(i%2)*2) // 偶索引拒收、奇索引接收
		if err != nil {
			t.Fatal(err)
		}
		if i == len(ids)-1 && st != lot.Rejected {
			t.Fatalf("T9 want rejected, got %v", st)
		}
	}
	if sev, _ := in.Severity(k); sev != plan.Suspended {
		t.Fatalf("want Suspended, got %v", sev)
	}
	if _, err := in.Submit(insp, k, "X", 60); !errors.Is(err, lot.ErrSuspended) {
		t.Fatalf("want ErrSuspended, got %v", err)
	}
	if err := in.Resume(insp, k); !errors.Is(err, lot.ErrUnauthorized) {
		t.Fatalf("inspector resume: %v", err)
	}
	if err := in.Resume(mgr, k); err != nil {
		t.Fatal(err)
	}
	if sev, _ := in.Severity(k); sev != plan.Tightened {
		t.Fatalf("resume want Tightened, got %v", sev)
	}
	in.Submit(insp, k, "R1", 60)
	in.Record(insp, "R1", 2)
	if sev, _ := in.Severity(k); sev != plan.Tightened {
		t.Fatalf("counters must be cleared, sev=%v", sev)
	}
}

func TestReinspection(t *testing.T) {
	in := lot.NewInspector(exampleTable(t))
	k := keyOf(0)
	dmap := map[string]int{"B": 2, "F": 2}
	for _, id := range []string{"A", "B", "C", "D", "E", "F"} {
		in.Submit(insp, k, id, 60)
		in.Record(insp, id, dmap[id])
	}
	in.Submit(insp, k, "RJ", 60)
	if _, st, _ := in.Record(insp, "RJ", 2); st != lot.Rejected {
		t.Fatalf("want Rejected")
	}
	if err := in.Resubmit(mgr, "RJ"); !errors.Is(err, lot.ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
	if err := in.Resubmit(insp, "RJ"); err != nil {
		t.Fatal(err)
	}
	rl, _ := in.Lookup("RJ")
	if rl.Scheme != (lot.Scheme{N: 20, Ac: 1, Re: 2}) || rl.Status != lot.Pending {
		t.Fatalf("reinspection snapshot: %+v", rl)
	}
	// 复检期间该流不能再 Submit
	if _, err := in.Submit(insp, k, "BUSY", 60); !errors.Is(err, lot.ErrConflictState) {
		t.Fatalf("want state conflict, got %v", err)
	}
	if _, st, _ := in.Record(insp, "RJ", 1); st != lot.Released {
		t.Fatalf("reinspection accept want Released, got %v", st)
	}
	if err := in.Resubmit(insp, "RJ"); !errors.Is(err, lot.ErrConflictState) {
		t.Fatalf("second resubmit: %v", err)
	}
	if sev, _ := in.Severity(k); sev != plan.Tightened {
		t.Fatalf("reinspection must not count, sev=%v", sev)
	}

	in.Submit(insp, k, "RJ2", 60)
	in.Record(insp, "RJ2", 2)
	for _, id := range []string{"S1", "S2", "S3", "S4"} {
		in.Submit(insp, k, id, 60)
		in.Record(insp, id, 2)
	}
	if sev, _ := in.Severity(k); sev != plan.Suspended {
		t.Fatalf("want suspended, got %v", sev)
	}
	if err := in.Resubmit(insp, "RJ2"); err != nil {
		t.Fatalf("resubmit while suspended: %v", err)
	}
	if _, st, _ := in.Record(insp, "RJ2", 2); st != lot.Scrapped {
		t.Fatalf("reinspection reject want Scrapped, got %v", st)
	}
	if sev, _ := in.Severity(k); sev != plan.Suspended {
		t.Fatalf("reinspection must not lift suspension, got %v", sev)
	}
}

func TestSampleClamp(t *testing.T) {
	in := lot.NewInspector(exampleTable(t))
	l, err := in.Submit(insp, keyOf(0), "C1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if l.Scheme.N != 10 {
		t.Fatalf("normal clamp n: %d", l.Scheme.N)
	}
	in.Record(insp, "C1", 2)
	in.Resubmit(insp, "C1")
	rl, _ := in.Lookup("C1")
	if rl.Scheme.N != 10 || rl.Scheme.Ac != 1 {
		t.Fatalf("reinspection clamp: %+v", rl.Scheme)
	}
	if _, _, err := in.Record(insp, "C1", 11); !errors.Is(err, lot.ErrCountExceedsN) {
		t.Fatalf("d>n want ErrCountExceedsN, got %v", err)
	}
}

func TestRejectionPrecedence(t *testing.T) {
	in := lot.NewInspector(exampleTable(t))
	k := keyOf(0)
	// Submit: 空标识（参数非法）先于 Manager 无权限
	if _, err := in.Submit(lot.Operator{ID: "", Role: lot.RoleManager}, k, "Z", 60); !errors.Is(err, lot.ErrInvalidArgument) {
		t.Fatalf("submit empty id: %v", err)
	}
	// N 越界
	if _, err := in.Submit(insp, k, "Z", 0); !errors.Is(err, lot.ErrInvalidArgument) {
		t.Fatalf("n out of range: %v", err)
	}
	// 无权限
	if _, err := in.Submit(mgr, k, "Z", 60); !errors.Is(err, lot.ErrUnauthorized) {
		t.Fatalf("submit unauthorized: %v", err)
	}
	// 批号冲突先于重复建立：正常提交一批未判定，再用同号
	in.Submit(insp, k, "DUP", 60)
	if _, err := in.Submit(insp, k, "DUP", 60); !errors.Is(err, lot.ErrConflictState) {
		t.Fatalf("open lot must be state conflict before dup check: %v", err)
	}
	in.Record(insp, "DUP", 0)
	if _, err := in.Submit(insp, k, "DUP", 60); !errors.Is(err, lot.ErrConflictID) {
		t.Fatalf("duplicate id: %v", err)
	}
	// Record: 参数非法 > 无权限 > 不存在 > 状态不符 > 数量越界
	if _, _, err := in.Record(lot.Operator{ID: "", Role: lot.RoleManager}, "NOPE", -1); !errors.Is(err, lot.ErrInvalidArgument) {
		t.Fatalf("record invalid: %v", err)
	}
	if _, _, err := in.Record(mgr, "NOPE", 0); !errors.Is(err, lot.ErrUnauthorized) {
		t.Fatalf("record unauthorized: %v", err)
	}
	if _, _, err := in.Record(insp, "NOPE", 0); !errors.Is(err, lot.ErrNotFound) {
		t.Fatalf("record not found: %v", err)
	}
	if _, _, err := in.Record(insp, "DUP", 0); !errors.Is(err, lot.ErrConflictState) {
		t.Fatalf("record already judged: %v", err)
	}
	in.Submit(insp, k, "BIG", 60)
	if _, _, err := in.Record(insp, "BIG", 14); !errors.Is(err, lot.ErrCountExceedsN) {
		t.Fatalf("d>n: %v", err)
	}
	in.Record(insp, "BIG", 0)
	// Resubmit: 不存在 > 状态不符（非 Rejected）
	if err := in.Resubmit(insp, "GHOST"); !errors.Is(err, lot.ErrNotFound) {
		t.Fatalf("resubmit not found: %v", err)
	}
	if err := in.Resubmit(insp, "BIG"); !errors.Is(err, lot.ErrConflictState) {
		t.Fatalf("resubmit non-rejected: %v", err)
	}
	// Resume: 无权限 > 不存在 > 状态不符
	if err := in.Resume(lot.Operator{ID: "", Role: lot.RoleInspector}, keyOf(1)); !errors.Is(err, lot.ErrInvalidArgument) {
		t.Fatalf("resume invalid: %v", err)
	}
	if err := in.Resume(insp, keyOf(1)); !errors.Is(err, lot.ErrUnauthorized) {
		t.Fatalf("resume unauthorized: %v", err)
	}
	if err := in.Resume(mgr, keyOf(1)); !errors.Is(err, lot.ErrNotFound) {
		t.Fatalf("resume not found: %v", err)
	}
	if err := in.Resume(mgr, k); !errors.Is(err, lot.ErrConflictState) {
		t.Fatalf("resume not suspended: %v", err)
	}
	// 被拒绝操作不改状态：DUP 仍 Released
	if rl, _ := in.Lookup("DUP"); rl.Status != lot.Released {
		t.Fatalf("rejected ops must not mutate: %v", rl.Status)
	}
}

func TestConcurrentStreams(t *testing.T) {
	in := lot.NewInspector(exampleTable(t))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k := lot.StreamKey{Supplier: "S", Material: string(rune('a' + g))}
			for i := 0; i < 30; i++ {
				id := string(rune('a'+g)) + "-" + string(rune('A'+i/26)) + string(rune('A'+i%26))
				if _, err := in.Submit(insp, k, id, 60); err != nil {
					t.Errorf("submit: %v", err)
					return
				}
				if _, _, err := in.Record(insp, id, i%2); err != nil {
					t.Errorf("record: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
