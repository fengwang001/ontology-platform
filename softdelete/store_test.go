package softdelete

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestCreateAndDefaultQueryFilter(t *testing.T) {
	s := New(nil)
	if _, err := s.Create("a", 1); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := s.Create("b", 2); err != nil {
		t.Fatalf("create b: %v", err)
	}

	if got := s.List(QueryOptions{}); len(got) != 2 || got[0].Key != "a" || got[1].Key != "b" {
		t.Fatalf("default list = %+v, want alive a,b sorted", got)
	}

	if _, err := s.SoftDelete("a"); err != nil {
		t.Fatalf("soft delete a: %v", err)
	}

	if got := s.List(QueryOptions{}); len(got) != 1 || got[0].Key != "b" {
		t.Fatalf("default list after soft delete = %+v, want only b", got)
	}
	if _, err := s.Get("a", QueryOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("default get on soft-deleted = %v, want ErrNotFound", err)
	}

	got := s.List(QueryOptions{IncludeDeleted: true})
	if len(got) != 2 {
		t.Fatalf("include-deleted list = %+v, want 2 records", got)
	}
	hidden, err := s.Get("a", QueryOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("include-deleted get: %v", err)
	}
	if hidden.DeletedAt.IsZero() {
		t.Fatal("soft-deleted object must carry a delete marker (DeletedAt)")
	}
}

func TestSoftDeleteStillOccupiesUniqueKey(t *testing.T) {
	s := New(nil)
	if _, err := s.Create("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDelete("k"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create("k", "v2"); !errors.Is(err, ErrKeyOccupied) {
		t.Fatalf("recreate soft-deleted key = %v, want ErrKeyOccupied", err)
	}
	if st, err := s.StateOf("k"); err != nil || st != StateDeleted {
		t.Fatalf("state after rejected recreate = %v,%v, want deleted unchanged", st, err)
	}
	if objs := s.List(QueryOptions{IncludeDeleted: true}); len(objs) != 1 || objs[0].Data != "v1" {
		t.Fatalf("rejected recreate must not replace data, got %+v", objs)
	}
}

func TestPurgeReleasesKeyAndRebuildAllowed(t *testing.T) {
	s := New(nil)
	if _, err := s.Create("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDelete("k"); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge("k"); err != nil {
		t.Fatalf("purge soft-deleted: %v", err)
	}
	if st, _ := s.StateOf("k"); st != StatePurged {
		t.Fatalf("state = %v, want purged", st)
	}
	if _, err := s.Get("k", QueryOptions{IncludeDeleted: true}); !errors.Is(err, ErrPhysicallyDeleted) {
		t.Fatalf("get purged = %v, want ErrPhysicallyDeleted", err)
	}

	rebuilt, err := s.Create("k", "v2")
	if err != nil {
		t.Fatalf("recreate after purge: %v", err)
	}
	if rebuilt.Data != "v2" || !rebuilt.DeletedAt.IsZero() {
		t.Fatalf("rebuilt object = %+v", rebuilt)
	}
	if got := s.List(QueryOptions{}); len(got) != 1 || got[0].Data != "v2" {
		t.Fatalf("list after rebuild = %+v, want v2", got)
	}
}

func TestRestoreRecoversVisibilityAndKey(t *testing.T) {
	s := New(nil)
	obj, err := s.Create("k", "v")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDelete("k"); err != nil {
		t.Fatal(err)
	}

	restored, err := s.Restore("k")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !restored.DeletedAt.IsZero() {
		t.Fatal("restored object must have delete marker cleared")
	}
	if st, _ := s.StateOf("k"); st != StateAlive {
		t.Fatalf("state after restore = %v, want alive", st)
	}
	if got, err := s.Get("k", QueryOptions{}); err != nil || got.Key != "k" {
		t.Fatalf("restored object must be visible by default query, got %+v,%v", got, err)
	}
	if !restored.CreatedAt.Equal(obj.CreatedAt) {
		t.Fatal("restore must revive the same record, not recreate it")
	}
	// 复活后唯一键仍归该对象占用，同键重建依旧被拒。
	if _, err := s.Create("k", "other"); !errors.Is(err, ErrKeyOccupied) {
		t.Fatalf("create after restore = %v, want ErrKeyOccupied", err)
	}
}

func TestRejectedOperationsAreDistinguishableAndStatePreserving(t *testing.T) {
	s := New(nil)

	if _, err := s.SoftDelete("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("soft-delete missing = %v, want ErrNotFound", err)
	}
	if _, err := s.Restore("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore missing = %v, want ErrNotFound", err)
	}
	if err := s.Purge("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purge missing = %v, want ErrNotFound", err)
	}

	if _, err := s.Create("k", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDelete("k"); err != nil {
		t.Fatal(err)
	}
	deleted, _ := s.Get("k", QueryOptions{IncludeDeleted: true})

	if _, err := s.SoftDelete("k"); !errors.Is(err, ErrAlreadyDeleted) {
		t.Fatalf("double soft-delete = %v, want ErrAlreadyDeleted", err)
	}
	after, _ := s.Get("k", QueryOptions{IncludeDeleted: true})
	if !after.DeletedAt.Equal(deleted.DeletedAt) {
		t.Fatal("rejected soft-delete must not move the delete marker")
	}

	if _, err := s.Restore("k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Restore("k"); !errors.Is(err, ErrNotDeleted) {
		t.Fatalf("restore alive = %v, want ErrNotDeleted", err)
	}
	if st, _ := s.StateOf("k"); st != StateAlive {
		t.Fatal("rejected restore must not change alive state")
	}

	if err := s.Purge("k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDelete("k"); !errors.Is(err, ErrPhysicallyDeleted) {
		t.Fatalf("soft-delete purged = %v, want ErrPhysicallyDeleted", err)
	}
	if _, err := s.Restore("k"); !errors.Is(err, ErrPhysicallyDeleted) {
		t.Fatalf("restore purged = %v, want ErrPhysicallyDeleted", err)
	}
	if err := s.Purge("k"); !errors.Is(err, ErrPhysicallyDeleted) {
		t.Fatalf("double purge = %v, want ErrPhysicallyDeleted", err)
	}
	if st, _ := s.StateOf("k"); st != StatePurged {
		t.Fatal("rejected operations on purged object must leave the tombstone")
	}
}

func TestConcurrentSoftDeletesConverge(t *testing.T) {
	for round := 0; round < 50; round++ {
		s := New(nil)
		if _, err := s.Create("k", round); err != nil {
			t.Fatal(err)
		}
		const n = 16
		var wg sync.WaitGroup
		errs := make([]error, n)
		start := make(chan struct{})
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func(i int) {
				defer wg.Done()
				<-start
				_, errs[i] = s.SoftDelete("k")
			}(i)
		}
		close(start)
		wg.Wait()

		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else if !errors.Is(err, ErrAlreadyDeleted) {
				t.Fatalf("unexpected concurrent soft-delete error: %v", err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: successful soft-deletes = %d, want exactly 1", round, ok)
		}
		if st, _ := s.StateOf("k"); st != StateDeleted {
			t.Fatalf("round %d: final state = %v, want deleted", round, st)
		}
	}
}

func TestConcurrentRestoresConverge(t *testing.T) {
	for round := 0; round < 50; round++ {
		s := New(nil)
		if _, err := s.Create("k", round); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SoftDelete("k"); err != nil {
			t.Fatal(err)
		}
		const n = 16
		var wg sync.WaitGroup
		errs := make([]error, n)
		start := make(chan struct{})
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func(i int) {
				defer wg.Done()
				<-start
				_, errs[i] = s.Restore("k")
			}(i)
		}
		close(start)
		wg.Wait()

		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else if !errors.Is(err, ErrNotDeleted) {
				t.Fatalf("unexpected concurrent restore error: %v", err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: successful restores = %d, want exactly 1", round, ok)
		}
		if st, _ := s.StateOf("k"); st != StateAlive {
			t.Fatalf("round %d: final state = %v, want alive", round, st)
		}
	}
}

// TestConcurrentDeleteAndRestoreNoIntermediateState 混合并发：状态只能在
// alive/deleted 间原子翻转；最终状态由成功次数差唯一决定，与调度顺序无关，
// 任何时刻都不存在“半删除”等中间态。
func TestConcurrentDeleteAndRestoreNoIntermediateState(t *testing.T) {
	for round := 0; round < 50; round++ {
		s := New(nil)
		if _, err := s.Create("k", round); err != nil {
			t.Fatal(err)
		}
		const n = 24
		var wg sync.WaitGroup
		var successDel, successRes int
		var mu sync.Mutex
		start := make(chan struct{})
		wg.Add(n)
		for i := 0; i < n; i++ {
			if i%2 == 0 {
				go func() {
					defer wg.Done()
					<-start
					if _, err := s.SoftDelete("k"); err == nil {
						mu.Lock()
						successDel++
						mu.Unlock()
					} else if !errors.Is(err, ErrAlreadyDeleted) {
						t.Errorf("unexpected delete error: %v", err)
					}
				}()
			} else {
				go func() {
					defer wg.Done()
					<-start
					if _, err := s.Restore("k"); err == nil {
						mu.Lock()
						successRes++
						mu.Unlock()
					} else if !errors.Is(err, ErrNotDeleted) {
						t.Errorf("unexpected restore error: %v", err)
					}
				}()
			}
		}
		close(start)
		wg.Wait()

		st, err := s.StateOf("k")
		if err != nil {
			t.Fatal(err)
		}
		// 从 alive 起合法翻转严格交替，成功次数差只能是 0（终于 alive）或 1（终于 deleted）。
		switch {
		case successDel == successRes && st != StateAlive:
			t.Fatalf("round %d: del=%d res=%d state=%v, want alive", round, successDel, successRes, st)
		case successDel == successRes+1 && st != StateDeleted:
			t.Fatalf("round %d: del=%d res=%d state=%v, want deleted", round, successDel, successRes, st)
		case successDel-successRes != 0 && successDel-successRes != 1:
			t.Fatalf("round %d: impossible success counts del=%d res=%d", round, successDel, successRes)
		}
	}
}

func TestLogsRecordObjectOpAndBasis(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf)
	if _, err := s.Create("log-k", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDelete("log-k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SoftDelete("log-k"); err == nil {
		t.Fatal("second soft-delete must be rejected")
	}
	if _, err := s.Restore("never"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}

	logs := buf.String()
	for _, want := range []string{
		`object="log-k"`,
		"op=create",
		"op=soft-delete",
		"op=restore",
		"decision=allow",
		"decision=reject",
		"basis=",
		`reason="` + ErrAlreadyDeleted.Error() + `"`,
		`reason="` + ErrNotFound.Error() + `"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %q\nfull logs:\n%s", want, logs)
		}
	}
}
