package backfill

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"ontology/ontology/migration"
	"ontology/ontology/router"
)

func newService(t *testing.T) *router.Service {
	t.Helper()
	svc := router.NewService(migration.NewDeclaration())
	if err := svc.AmendDeclaration([]migration.Mapping{
		{Property: "name", Action: migration.ActionRetain},
		{Property: "nick", Action: migration.ActionDeprecate},
		{Property: "age", Action: migration.ActionAddDefault, Default: 18},
	}); err != nil {
		t.Fatal(err)
	}
	return svc
}

// Worker 按确定性的内部顺序(字典序)逐个回填,全部处理后报告 Done。
func TestWorkerDrainsInDeterministicOrder(t *testing.T) {
	svc := newService(t)
	for _, id := range []string{"c", "a", "b"} {
		if err := svc.Create(id, router.VersionOld, map[string]any{"name": id, "nick": "n"}); err != nil {
			t.Fatal(err)
		}
	}
	w := NewWorker(svc)
	var order []string
	for {
		r := w.Step()
		if r.Done {
			break
		}
		if r.Outcome != router.BackfillApplied {
			t.Fatalf("step %s: outcome %v", r.ID, r.Outcome)
		}
		order = append(order, r.ID)
	}
	if !reflect.DeepEqual(order, []string{"a", "b", "c"}) {
		t.Fatalf("backfill order = %v, want lexicographic [a b c]", order)
	}
	t.Logf("依据: 回填内部顺序为实例 ID 字典序, 实际顺序 %v", order)
}

// 回填与并发写入竞争:被抢先的实例跳过而非覆盖,其余正常回填。
func TestWorkerSkipsConcurrentlyWrittenInstances(t *testing.T) {
	svc := newService(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := svc.Create(id, router.VersionOld, map[string]any{"name": id}); err != nil {
			t.Fatal(err)
		}
	}
	// b 被一次正常写入抢先回填。
	if err := svc.Write("b", router.VersionNew, map[string]any{"name": "B"}); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(svc)
	results := w.RunUntilDrained()
	outcomes := map[string]router.BackfillOutcome{}
	for _, r := range results {
		outcomes[r.ID] = r.Outcome
	}
	if outcomes["a"] != router.BackfillApplied || outcomes["c"] != router.BackfillApplied {
		t.Fatalf("a/c should be applied: %v", outcomes)
	}
	if len(results) != 2 {
		t.Fatalf("b was already backfilled by the write and must not appear as a backfill step: %v", results)
	}
	got, _ := svc.Read("b", router.VersionNew)
	if got["name"] != "B" {
		t.Fatalf("concurrent write was overwritten by backfill: %v", got)
	}
	t.Logf("输入: b 被写入抢先回填后运行回填; 实际输出: steps=%v, b 视图=%v; 依据: 已回填实例不出现在回填步骤中", results, got)
}

// 异步 Run 与并发读写交错,最终所有实例一致地回填完成。
func TestWorkerRunAsyncWithConcurrentTraffic(t *testing.T) {
	svc := newService(t)
	const n = 16
	for i := 0; i < n; i++ {
		if err := svc.Create(fmt.Sprintf("i%02d", i), router.VersionOld, map[string]any{"name": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := NewWorker(svc)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Run(ctx, time.Millisecond)
	}()
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("i%02d", i%n)
		_ = svc.Write(id, router.VersionNew, map[string]any{"name": fmt.Sprintf("v%d", i)})
		_, _ = svc.Read(id, router.VersionOld)
	}
	// 等回填 worker 自然结束(全部回填完)。
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not drain")
	}
	if pending := svc.PendingIDs(); len(pending) != 0 {
		t.Fatalf("still pending: %v", pending)
	}
}
