package recovery

import (
	"errors"
	"testing"

	"ontology/history"
	"ontology/lease"
)

func errKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, history.ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, history.ErrClockBack):
		return "clockback"
	case errors.Is(err, history.ErrDocNotFound):
		return "notfound"
	case errors.Is(err, lease.ErrLeaseExists):
		return "leaseexists"
	case errors.Is(err, lease.ErrLeaseNotFound):
		return "leasenotfound"
	case errors.Is(err, lease.ErrLeaseBack):
		return "leaseback"
	case errors.Is(err, lease.ErrHistoryUnavail):
		return "unavail"
	case errors.Is(err, lease.ErrLeaseLimit):
		return "limit"
	case errors.Is(err, lease.ErrCheckpointBack):
		return "gcpback"
	default:
		return "unknown:" + err.Error()
	}
}

func expectSeq(t *testing.T, seq int64, e error, want int64) {
	t.Helper()
	if e != nil || seq != want {
		t.Fatalf("want seq %d, got %d err %v", want, seq, e)
	}
}

func idx(t *testing.T, h *history.History, now int64, id string, want int64) {
	t.Helper()
	seq, e := h.Index(now, []byte(id))
	expectSeq(t, seq, e, want)
}

func del(t *testing.T, h *history.History, now int64, id string, want int64) {
	t.Helper()
	seq, e := h.Delete(now, []byte(id))
	expectSeq(t, seq, e, want)
}

// TestSpecExamples 复现题面两个示例（含分叉）。
func TestSpecExamples(t *testing.T) {
	h := history.NewHistory()
	s, err := lease.NewSet(h, 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPlanner(h)
	idx(t, h, 0, "a", 1)
	idx(t, h, 0, "b", 2)
	del(t, h, 0, "a", 3)
	idx(t, h, 0, "b", 4)
	idx(t, h, 0, "c", 5)
	if err := s.AddLease(0, "L1", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGlobalCheckpoint(0, 4); err != nil {
		t.Fatal(err)
	}
	r, err := s.Merge(10)
	if err != nil || r.Cleared != 1 || len(r.RemovedLeases) != 0 {
		t.Fatalf("Merge(10)=%+v,%v", r, err)
	}
	if pl, err := p.Plan(1); err != nil || pl.Mode != OpsBased || len(pl.Ops) != 4 ||
		pl.Ops[0].Seq != 2 || pl.Ops[3].Seq != 5 {
		t.Fatalf("Plan(1)=%+v,%v", pl, err)
	}
	if pl, _ := p.Plan(0); pl.Mode != FileBased || len(pl.Docs) != 2 ||
		string(pl.Docs[0].ID) != "b" || pl.Docs[0].Seq != 4 ||
		string(pl.Docs[1].ID) != "c" || pl.Docs[1].Seq != 5 || pl.MaxSeq != 5 {
		t.Fatalf("Plan(0)=%+v", pl)
	}
	if r, err := s.Merge(100); err != nil || r.Cleared != 0 {
		t.Fatalf("Merge(100)=%+v,%v", r, err)
	}
	r, err = s.Merge(101)
	if err != nil || r.Cleared != 2 {
		t.Fatalf("Merge(101)=%+v,%v want cleared 2", r, err)
	}
	if pl, _ := p.Plan(3); pl.Mode != FileBased {
		t.Fatalf("Plan(3) mode=%d want FileBased", pl.Mode)
	}
	if pl, _ := p.Plan(4); pl.Mode != OpsBased || len(pl.Ops) != 1 || pl.Ops[0].Seq != 5 {
		t.Fatalf("Plan(4)=%+v want only seq5", pl)
	}

	// 分叉：过期但未剔除可续活。
	h2 := history.NewHistory()
	s2, _ := lease.NewSet(h2, 100, 10)
	p2 := NewPlanner(h2)
	idx(t, h2, 0, "a", 1)
	idx(t, h2, 0, "b", 2)
	del(t, h2, 0, "a", 3)
	idx(t, h2, 0, "b", 4)
	idx(t, h2, 0, "c", 5)
	_ = s2.AddLease(0, "L1", 2)
	_ = s2.SetGlobalCheckpoint(0, 4)
	if _, e := s2.Merge(10); e != nil {
		t.Fatal(e)
	}
	if _, e := s2.Merge(100); e != nil {
		t.Fatal(e)
	}
	if err := s2.RenewLease(101, "L1", 3); err != nil {
		t.Fatalf("expired-not-purged renew: %v", err)
	}
	r2, err := s2.Merge(101)
	if err != nil || r2.Cleared != 1 {
		t.Fatalf("branch Merge(101)=%+v,%v want 1", r2, err)
	}
	if err := s2.AddLease(101, "L2", 2); !errors.Is(err, lease.ErrHistoryUnavail) {
		t.Fatalf("r<H want unavail, got %v", err)
	}
	if err := s2.AddLease(101, "L2", 3); err != nil {
		t.Fatalf("r==H add: %v", err)
	}
	if pl, _ := p2.Plan(2); pl.Mode != OpsBased || len(pl.Ops) != 3 ||
		pl.Ops[0].Seq != 3 || pl.Ops[1].Seq != 4 || pl.Ops[2].Seq != 5 {
		t.Fatalf("branch Plan(2)=%+v want seq3,4,5", pl)
	}
}
