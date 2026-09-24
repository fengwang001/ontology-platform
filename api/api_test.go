package api

import (
	"errors"
	"math/rand"
	"ontology/rc"
	"ontology/txlog"
	"slices"
	"sync"
	"testing"
)

func applyOne(svc *Service, o txlog.Record, recs []txlog.Record) ([]txlog.Record, error) {
	if o.Kind < 0 { // Kind=-1 表示 AdvanceHW(Pid)
		return recs, svc.AdvanceHW(o.Pid)
	}
	if _, err := appendOp(svc, o); err != nil {
		return recs, err
	}
	return append(recs, o), nil
}
func randomOps(seed int64, n int) []txlog.Record { // 循环生成随机交错的合法操作序列
	rng := rand.New(rand.NewSource(seed))
	var ops []txlog.Record
	open := map[int]bool{}
	end, hw := 0, 0
	for len(ops) < n {
		pid := rng.Intn(4) + 1
		switch r := rng.Intn(6); {
		case r < 3:
			ops = append(ops, txlog.Record{Pid: pid, Val: string(rune('a' + end%26))})
			open[pid], end = true, end+1
		case r < 5 && open[pid]:
			ops = append(ops, txlog.Record{Pid: pid, Kind: txlog.Kind(r - 3)})
			delete(open, pid)
			end++
		default:
			hw += rng.Intn(end - hw + 1)
			ops = append(ops, txlog.Record{Kind: -1, Pid: hw})
		}
	}
	return ops
}
func scriptedSvc(t *testing.T) *Service {
	svc := New()
	for _, r := range script {
		if _, err := appendOp(svc, r); err != nil {
			t.Fatal(err)
		}
	}
	return svc
}
func TestFetchMatchesBatchReference(t *testing.T) {
	for seed := range int64(6) {
		svc := New()
		var recs []txlog.Record
		var got []string
		from := 0
		for _, o := range randomOps(seed, 300) {
			recs, _ = applyOne(svc, o, recs)
			out, next, err := svc.Fetch(from)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range out {
				got = append(got, r.Val)
			}
			from = next
		}
		if want := batchCommitted(recs, svc.HW(), svc.LSO()); !slices.Equal(got, want) {
			t.Fatalf("seed=%d got=%v want=%v", seed, got, want)
		}
	}
}
func TestLSOLegalMonotone(t *testing.T) {
	svc := scriptedSvc(t)
	prev := 0
	for i, h := range []int{2, 4, 6, 7, 9, 10, 11} {
		_ = svc.AdvanceHW(h)
		if lso, want := svc.LSO(), []int{0, 1, 1, 4, 4, 8, 11}[i]; lso != want || lso > svc.HW() || lso < prev {
			t.Fatalf("步 %d: LSO=%d want=%d", i+1, lso, want)
		}
		prev = svc.LSO()
	}
}
func TestNoUndecidedOrAbortedExposed(t *testing.T) {
	svc := scriptedSvc(t)
	_ = svc.AdvanceHW(11)
	out, next, err := svc.Fetch(0)
	if err != nil || next != 11 {
		t.Fatalf("next=%d err=%v", next, err)
	}
	for _, r := range out {
		if r.Kind != txlog.Data || !r.Committed {
			t.Fatalf("暴露 %+v", r)
		}
	}
}
func TestRejectedOpsLeaveStateUnchanged(t *testing.T) {
	svc := New()
	_, _ = svc.AppendData(1, "x")
	_ = svc.AdvanceHW(1)
	for _, from := range []int{-1, 2} {
		if _, _, err := svc.Fetch(from); !errors.Is(err, rc.ErrInvalidFrom) {
			t.Fatalf("from=%d: %v", from, err)
		}
	}
	if svc.HW() != 1 || svc.LSO() != 0 {
		t.Fatal("被拒后状态改变")
	}
}
func TestConcurrentConsumers(t *testing.T) {
	svc := scriptedSvc(t)
	var wg sync.WaitGroup
	outs := make([][]string, 8)
	for i := range outs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for from, prev := 0, -1; ; from = prev {
				out, next, err := svc.Fetch(from)
				if err != nil || next < prev {
					t.Errorf("from=%d next=%d err=%v", from, next, err)
					return
				}
				prev = next
				for _, r := range out {
					outs[i] = append(outs[i], r.Val)
				}
				if next == 11 {
					if !slices.Equal(outs[i], []string{"a", "c", "d", "f"}) {
						t.Errorf("got=%v", outs[i])
					}
					return
				}
			}
		}()
	}
	for _, h := range []int{2, 4, 6, 7, 9, 10, 11} {
		_ = svc.AdvanceHW(h)
	}
	wg.Wait()
}
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
