package undo

import (
	"errors"
	"sync"
	"testing"
)

func TestSpecLifecycleAndReuse(t *testing.T) {
	m, err := NewManager(3, 4, 100)
	if err != nil {
		t.Fatal(err)
	}

	mustBegin(t, m, 1)
	for range 2 {
		if err := m.Modify(1); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Insert(1); err != nil {
		t.Fatal(err)
	}
	if got := m.usedSlots; got != 2 {
		t.Fatalf("usedSlots = %d, want 2", got)
	}

	if trxNo, err := m.Commit(1); err != nil || trxNo != 1 {
		t.Fatalf("Commit = (%d, %v), want (1, nil)", trxNo, err)
	}
	if len(m.caches[insertUndo]) != 1 || len(m.history) != 1 {
		t.Fatalf("after commit: insert cache=%d history=%d", len(m.caches[insertUndo]), len(m.history))
	}

	view := m.OpenView()
	if got := m.views[view]; got != 2 {
		t.Fatalf("view limit = %d, want 2", got)
	}

	mustBegin(t, m, 2)
	if err := m.Modify(2); err != nil {
		t.Fatal(err)
	}
	if trxNo, err := m.Commit(2); err != nil || trxNo != 2 {
		t.Fatalf("Commit = (%d, %v), want (2, nil)", trxNo, err)
	}

	got, err := m.Purge(10)
	if err != nil || len(got) != 1 || got[0] != 1 {
		t.Fatalf("Purge = %v, %v; want [1]", got, err)
	}
	if len(m.caches[updateUndo]) != 1 || len(m.history) != 1 {
		t.Fatalf("after first purge: update cache=%d history=%d", len(m.caches[updateUndo]), len(m.history))
	}

	if err := m.CloseView(view); err != nil {
		t.Fatal(err)
	}
	got, err = m.Purge(10)
	if err != nil || len(got) != 1 || got[0] != 2 {
		t.Fatalf("Purge = %v, %v; want [2]", got, err)
	}

	mustBegin(t, m, 3)
	if err := m.Modify(3); err != nil {
		t.Fatal(err)
	}
	if err := m.Insert(3); err != nil {
		t.Fatal(err)
	}
	if got := m.usedSlots; got != 3 {
		t.Fatalf("usedSlots after reuse = %d, want 3", got)
	}
	if got := m.usedPages; got != 3 {
		t.Fatalf("usedPages after reuse = %d, want 3", got)
	}
}

func TestCacheThresholdAndMultiPage(t *testing.T) {
	tests := []struct {
		name    string
		records int
		cached  bool
		pages   int
	}{
		{name: "three quarters cached", records: 3, cached: true, pages: 1},
		{name: "one above threshold released", records: 4, cached: false, pages: 1},
		{name: "multi page released", records: 5, cached: false, pages: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := NewManager(2, 4, 10)
			mustBegin(t, m, 1)
			for range tt.records {
				if err := m.Modify(1); err != nil {
					t.Fatal(err)
				}
			}
			seg := m.transactions[1].segments[updateUndo]
			if seg.pages != tt.pages {
				t.Fatalf("pages = %d, want %d", seg.pages, tt.pages)
			}
			if err := m.Rollback(1); err != nil {
				t.Fatal(err)
			}
			if got := len(m.caches[updateUndo]) == 1; got != tt.cached {
				t.Fatalf("cached = %v, want %v", got, tt.cached)
			}
		})
	}
}

func TestCacheOccupiesSlotForOtherKind(t *testing.T) {
	m, _ := NewManager(1, 10, 10)
	mustBegin(t, m, 1)
	if err := m.Modify(1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Purge(10); err != nil {
		t.Fatal(err)
	}

	mustBegin(t, m, 2)
	if err := m.Insert(2); !errors.Is(err, ErrNoFreeSlot) {
		t.Fatalf("Insert error = %v, want ErrNoFreeSlot", err)
	}

	mustBegin(t, m, 3)
	if err := m.Modify(3); err != nil {
		t.Fatal(err)
	}
	if m.usedSlots != 1 || m.usedPages != 1 {
		t.Fatalf("reuse used slots/pages = %d/%d, want 1/1", m.usedSlots, m.usedPages)
	}
}

func TestCacheLIFO(t *testing.T) {
	m, _ := NewManager(2, 10, 10)
	mustBegin(t, m, 1)
	if err := m.Modify(1); err != nil {
		t.Fatal(err)
	}
	first := m.transactions[1].segments[updateUndo]
	if _, err := m.Commit(1); err != nil {
		t.Fatal(err)
	}
	view := m.OpenView()
	mustBegin(t, m, 2)
	if err := m.Modify(2); err != nil {
		t.Fatal(err)
	}
	second := m.transactions[2].segments[updateUndo]
	if _, err := m.Commit(2); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseView(view); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Purge(10); err != nil {
		t.Fatal(err)
	}

	mustBegin(t, m, 3)
	if err := m.Modify(3); err != nil {
		t.Fatal(err)
	}
	reused := m.transactions[3].segments[updateUndo]
	if reused != second {
		t.Fatalf("reused %p, want most recently cached %p", reused, second)
	}
	if len(m.caches[updateUndo]) != 1 || m.caches[updateUndo][0] != first {
		t.Fatalf("remaining cache is not the older segment")
	}
}

func TestCommitRollbackViewAndPurgeRules(t *testing.T) {
	m, _ := NewManager(5, 10, 20)
	mustBegin(t, m, 1)
	if trxNo, err := m.Commit(1); err != nil || trxNo != 1 {
		t.Fatalf("empty Commit = (%d, %v), want 1", trxNo, err)
	}
	mustBegin(t, m, 2)
	if err := m.Modify(2); err != nil {
		t.Fatal(err)
	}
	if err := m.Rollback(2); err != nil {
		t.Fatal(err)
	}
	if m.commits != 1 || len(m.history) != 0 {
		t.Fatalf("rollback consumed commit or retained history")
	}

	mustBegin(t, m, 3)
	if err := m.Modify(3); err != nil {
		t.Fatal(err)
	}
	if trxNo, err := m.Commit(3); err != nil || trxNo != 2 {
		t.Fatalf("Commit = (%d, %v), want 2", trxNo, err)
	}
	v1 := m.OpenView()

	mustBegin(t, m, 4)
	if err := m.Modify(4); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(4); err != nil {
		t.Fatal(err)
	}
	v2 := m.OpenView()

	got, err := m.Purge(1)
	if err != nil || len(got) != 1 || got[0] != 2 {
		t.Fatalf("bounded purge = %v, %v; want [2]", got, err)
	}
	got, err = m.Purge(10)
	if err != nil || len(got) != 0 {
		t.Fatalf("view limit blocks trx 3, got %v, %v", got, err)
	}
	if err := m.CloseView(v1); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseView(v2); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseView(v2); !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("double CloseView error = %v, want ErrViewNotFound", err)
	}
}

func TestRejectionOrderAndAtomicity(t *testing.T) {
	if _, err := NewManager(0, 4, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewManager error = %v", err)
	}

	m, _ := NewManager(1, 4, 1)
	if err := m.Insert(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid id error = %v", err)
	}
	if err := m.Insert(1); !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("missing transaction error = %v", err)
	}
	mustBegin(t, m, 1)
	if err := m.Begin(1); !errors.Is(err, ErrTransactionExists) {
		t.Fatalf("duplicate begin error = %v", err)
	}
	if err := m.Insert(1); err != nil {
		t.Fatal(err)
	}
	if err := m.Modify(1); !errors.Is(err, ErrNoFreeSlot) {
		t.Fatalf("slot error = %v, want no free slot", err)
	}
	if _, err := m.Commit(1); err != nil {
		t.Fatal(err)
	}
	if err := m.Insert(1); !errors.Is(err, ErrTransactionDone) {
		t.Fatalf("terminated error = %v", err)
	}

	m, _ = NewManager(2, 4, 1)
	mustBegin(t, m, 2)
	if err := m.Insert(2); err != nil {
		t.Fatal(err)
	}
	mustBegin(t, m, 3)
	if err := m.Modify(3); !errors.Is(err, ErrPageBudgetExceeded) {
		t.Fatalf("page budget error = %v", err)
	}
	if m.usedSlots != 1 || m.usedPages != 1 || len(m.transactions[3].segments[:]) != 2 {
		t.Fatalf("rejected allocation mutated state: slots=%d pages=%d", m.usedSlots, m.usedPages)
	}

	if _, err := m.Purge(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Purge(0) error = %v", err)
	}
}

func TestConcurrentOperations(t *testing.T) {
	m, _ := NewManager(1000, 1000, 100000)
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range 50 {
				trx := worker*50 + i + 1
				if err := m.Begin(trx); err != nil {
					t.Errorf("Begin: %v", err)
					return
				}
				if err := m.Modify(trx); err != nil {
					t.Errorf("Modify: %v", err)
					return
				}
				if _, err := m.Commit(trx); err != nil {
					t.Errorf("Commit: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	if got, err := m.Purge(10000); err != nil || len(got) != 400 {
		t.Fatalf("Purge len=%d err=%v, want 400", len(got), err)
	}
}

func mustBegin(t *testing.T, m *Manager, id int) {
	t.Helper()
	if err := m.Begin(id); err != nil {
		t.Fatalf("Begin(%d): %v", id, err)
	}
}
