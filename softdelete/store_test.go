package softdelete

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) (*Store[string, string], *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewWithLogger[string, string](logger), &buf
}

func TestCreateAndDefaultQueryFiltersSoftDeleted(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	obj, err := store.Create(ctx, "k1", "v1")
	if err != nil || obj.State != StateAlive || obj.Revision != 1 {
		t.Fatalf("Create returned obj=%v err=%v", obj, err)
	}

	if got, ok := store.Get(ctx, "k1", QueryOptions{}); !ok || got.State != StateAlive || got.Value != "v1" {
		t.Fatalf("default Get = %v, %v", got, ok)
	}

	if err := store.SoftDelete(ctx, "k1"); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	if _, ok := store.Get(ctx, "k1", QueryOptions{}); ok {
		t.Fatal("soft-deleted object must be hidden from default Get")
	}
	got, ok := store.Get(ctx, "k1", QueryOptions{IncludeDeleted: true})
	if !ok || got.State != StateDeleted || got.Value != "v1" {
		t.Fatalf("IncludeDeleted Get = %v, %v, want deleted record physically retained", got, ok)
	}

	defaultList := store.List(ctx, QueryOptions{})
	if len(defaultList) != 0 {
		t.Fatalf("default List must filter soft-deleted objects, got %d", len(defaultList))
	}
	allList := store.List(ctx, QueryOptions{IncludeDeleted: true})
	if len(allList) != 1 || allList[0].State != StateDeleted {
		t.Fatalf("IncludeDeleted List = %v, want the soft-deleted record", allList)
	}
}

func TestSoftDeletedKeyOccupiedUntilPurge(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	if _, err := store.Create(ctx, "k1", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SoftDelete(ctx, "k1"); err != nil {
		t.Fatal(err)
	}

	_, err := store.Create(ctx, "k1", "v2")
	if !errors.Is(err, ErrKeyOccupied) || ReasonOf(err) != ReasonKeyOccupied {
		t.Fatalf("recreate while soft-deleted must fail with key_occupied, got %v", err)
	}

	if err := store.Purge(ctx, "k1"); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	obj, err := store.Create(ctx, "k1", "v2")
	if err != nil || obj.Value != "v2" || obj.State != StateAlive {
		t.Fatalf("recreate after purge must succeed, obj=%v err=%v", obj, err)
	}
}

func TestRestoreRevivesAndOwnsUniqueKey(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	if _, err := store.Create(ctx, "k1", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SoftDelete(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Restore(ctx, "k1"); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, ok := store.Get(ctx, "k1", QueryOptions{})
	if !ok || got.State != StateAlive {
		t.Fatalf("restored object must be visible to default query, got %v, %v", got, ok)
	}
	if _, err := store.Create(ctx, "k1", "v2"); !errors.Is(err, ErrKeyOccupied) {
		t.Fatalf("restored object must keep occupying unique key, got err=%v", err)
	}
}

func TestRejectionReasonsAreDistinguishableAndStatePreserved(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	check := func(err error, want Reason) {
		t.Helper()
		if !errors.Is(err, &OpError{Reason: want}) || ReasonOf(err) != want {
			t.Fatalf("err=%v, want reason %q", err, want)
		}
	}

	check(store.SoftDelete(ctx, "ghost"), ReasonNotFound)
	check(store.Restore(ctx, "ghost"), ReasonNotFound)
	check(store.Purge(ctx, "ghost"), ReasonNotFound)

	if _, err := store.Create(ctx, "k1", "v1"); err != nil {
		t.Fatal(err)
	}
	check(store.Restore(ctx, "k1"), ReasonNotDeleted)

	if err := store.SoftDelete(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	check(store.SoftDelete(ctx, "k1"), ReasonAlreadyDeleted)

	if err := store.Purge(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	check(store.SoftDelete(ctx, "k1"), ReasonNotFound)
	check(store.Restore(ctx, "k1"), ReasonNotFound)
	check(store.Purge(ctx, "k1"), ReasonNotFound)

	// 被拒绝的重复软删不得改变状态；随后仍可正常复活。
	if _, err := store.Create(ctx, "k2", "v2"); err != nil {
		t.Fatal(err)
	}
	if err := store.SoftDelete(ctx, "k2"); err != nil {
		t.Fatal(err)
	}
	if err := store.SoftDelete(ctx, "k2"); err == nil {
		t.Fatal("second SoftDelete on deleted object must be rejected")
	}
	got, ok := store.Get(ctx, "k2", QueryOptions{IncludeDeleted: true})
	if !ok || got.State != StateDeleted {
		t.Fatalf("rejected operation must not change state, got %v, %v", got, ok)
	}
	if err := store.Restore(ctx, "k2"); err != nil {
		t.Fatalf("Restore after rejected duplicate delete: %v", err)
	}
}

func TestConcurrentSameOperationsConvergeToSingleState(t *testing.T) {
	ctx := context.Background()

	groups := []struct {
		name string
		run  func(store *Store[string, string], key string)
		want State
	}{
		{
			name: "parallel soft deletes",
			run: func(store *Store[string, string], key string) {
				runConcurrent(func() error { return store.SoftDelete(ctx, key) })
			},
			want: StateDeleted,
		},
		{
			name: "parallel restores on deleted",
			run: func(store *Store[string, string], key string) {
				if err := store.SoftDelete(ctx, key); err != nil {
					t.Fatal(err)
				}
				runConcurrent(func() error { return store.Restore(ctx, key) })
			},
			want: StateAlive,
		},
		{
			name: "parallel purges",
			run: func(store *Store[string, string], key string) {
				runConcurrent(func() error { return store.Purge(ctx, key) })
			},
			want: -1,
		},
	}

	for _, group := range groups {
		for i := 0; i < 50; i++ {
			store, _ := newTestStore(t)
			key := "k"
			if _, err := store.Create(ctx, key, "v"); err != nil {
				t.Fatal(err)
			}
			group.run(store, key)
			obj, ok := store.Get(ctx, key, QueryOptions{IncludeDeleted: true})
			if group.want < 0 {
				if ok {
					t.Fatalf("%s iteration %d: record must be purged, got %v", group.name, i, obj)
				}
				continue
			}
			if !ok || obj.State != group.want {
				t.Fatalf("%s iteration %d: state=%v ok=%v, want %v", group.name, i, obj, ok, group.want)
			}
		}
	}
}

// TestOperationBatchesConvergeRegardlessOfOrder 验证同一组操作以任意调度
// 顺序执行都得到相同最终状态，且只出现状态机内的合法状态，无中间态。
// 相互冲突的 delete/restore 属于“最后提交者胜”，此处只包含可交换的操作组。
func TestOperationBatchesConvergeRegardlessOfOrder(t *testing.T) {
	ctx := context.Background()

	t.Run("duplicate deletes converge", func(t *testing.T) {
		orders := [][]string{
			{"delete", "delete"},
		}
		for i := 0; i < 100; i++ {
			for _, order := range orders {
				store, _ := newTestStore(t)
				if _, err := store.Create(ctx, "k", "v"); err != nil {
					t.Fatal(err)
				}
				applyOrder(t, store, order)
				got, ok := store.Get(ctx, "k", QueryOptions{IncludeDeleted: true})
				if !ok || got.State != StateDeleted || got.Revision != 2 {
					t.Fatalf("order=%v iter=%d final=%v,%v want deleted with single effective transition",
						order, i, got, ok)
				}
			}
		}
	})

	t.Run("delete purge leaves key released", func(t *testing.T) {
		orders := [][]string{
			{"delete", "purge"},
			{"purge", "delete"},
		}
		for i := 0; i < 100; i++ {
			for _, order := range orders {
				store, _ := newTestStore(t)
				if _, err := store.Create(ctx, "k", "v"); err != nil {
					t.Fatal(err)
				}
				applyOrder(t, store, order)
				if _, ok := store.Get(ctx, "k", QueryOptions{IncludeDeleted: true}); ok {
					t.Fatalf("order=%v iter=%d: record must end purged", order, i)
				}
				if _, err := store.Create(ctx, "k", "v2"); err != nil {
					t.Fatalf("order=%v iter=%d: key must be released for recreate, got %v", order, i, err)
				}
			}
		}
	})

	t.Run("restore and purge converge to purged", func(t *testing.T) {
		orders := [][]string{
			{"restore", "purge"},
			{"purge", "restore"},
		}
		for i := 0; i < 100; i++ {
			for _, order := range orders {
				store, _ := newTestStore(t)
				if _, err := store.Create(ctx, "k", "v"); err != nil {
					t.Fatal(err)
				}
				applyOrder(t, store, order)
				if _, ok := store.Get(ctx, "k", QueryOptions{IncludeDeleted: true}); ok {
					t.Fatalf("order=%v iter=%d: record must end purged", order, i)
				}
				if _, err := store.Create(ctx, "k", "v2"); err != nil {
					t.Fatalf("order=%v iter=%d: key must be released, got %v", order, i, err)
				}
			}
		}
	})
}

func applyOrder(t *testing.T, store *Store[string, string], order []string) {
	t.Helper()
	ctx := context.Background()
	for _, op := range order {
		var err error
		switch op {
		case "delete":
			err = store.SoftDelete(ctx, "k")
		case "restore":
			err = store.Restore(ctx, "k")
		case "purge":
			err = store.Purge(ctx, "k")
		}
		if err != nil {
			// 不合法顺序（如复活存活对象）仅作为拒绝记录，最终状态仍需一致。
			var opErr *OpError
			if !errors.As(err, &opErr) {
				t.Fatalf("unexpected non-state error: %v", err)
			}
		}
	}
}

func TestRejectedOperationsProduceNoSideEffects(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)

	if _, err := store.Create(ctx, "k1", "v1"); err != nil {
		t.Fatal(err)
	}
	before := store.List(ctx, QueryOptions{IncludeDeleted: true})
	if err := store.SoftDelete(ctx, "missing"); err == nil {
		t.Fatal("expected rejection")
	}
	if err := store.Purge(ctx, "missing"); err == nil {
		t.Fatal("expected rejection")
	}
	after := store.List(ctx, QueryOptions{IncludeDeleted: true})
	if len(before) != len(after) || after[0].Key != "k1" || after[0].Revision != 1 {
		t.Fatalf("rejected ops changed state: before=%v after=%v", before, after)
	}
}

func TestLogsContainObjectOperationAndBasis(t *testing.T) {
	ctx := context.Background()
	store, buf := newTestStore(t)

	if _, err := store.Create(ctx, "k-log", "v"); err != nil {
		t.Fatal(err)
	}
	if err := store.SoftDelete(ctx, "k-log"); err != nil {
		t.Fatal(err)
	}
	if err := store.SoftDelete(ctx, "k-log"); err == nil {
		t.Fatal("expected already_deleted rejection")
	}
	if err := store.Restore(ctx, "missing"); err == nil {
		t.Fatal("expected not_found rejection")
	}

	logText := buf.String()
	for _, want := range []string{
		`op=create`,
		`op=soft_delete`,
		`key=k-log`,
		`decision=accepted`,
		`decision=rejected`,
		`reason=already_deleted`,
		`reason=not_found`,
		`basis=`,
		`object_state=deleted`,
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q\n--- logs ---\n%s", want, logText)
		}
	}
}

func runConcurrent(op func() error) {
	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_ = op()
		}()
	}
	wg.Wait()
}
