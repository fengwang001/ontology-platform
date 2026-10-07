package ontology

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// 拒绝优先级：未声明 > 重建中/失败不可用 > 可用但带不一致声明。
func TestQueryPriorityOrdering(t *testing.T) {
	_, mgr, _, _ := newHarness(t)
	mgr.Declare("T", "idx", "p")
	mgr.Write("T", "o1", map[string]PropertyValue{"p": "A"})

	if _, err := mgr.Query("T", "other", "A"); !errors.Is(err, ErrIndexNotDeclared) {
		t.Fatalf("want not-declared, got %v", err)
	}
	if _, err := mgr.Query("T", "idx", "A"); !errors.Is(err, ErrIndexUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatal(err)
	}
	res, err := mgr.Query("T", "idx", "A")
	if err != nil || res.InconsistencyDeclared {
		t.Fatalf("clean query res=%+v err=%v", res, err)
	}
}

// O(1) 取证：单条目复核读取的历史记录数不随对象历史长度增长。
func TestSingleEntryVerifyConstantHistoryReads(t *testing.T) {
	store, mgr, ver, _ := newHarness(t)
	mgr.Declare("T", "idx", "p")
	mgr.Write("T", "hot", map[string]PropertyValue{"p": "v0"})
	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatal(err)
	}

	measure := func(addWrites int) (reads int64, histLen int) {
		for i := 0; i < addWrites; i++ {
			mgr.Write("T", "hot",
				map[string]PropertyValue{"p": fmt.Sprintf("v%d", i%7)})
		}
		audit, err := mgr.Rebuild("T", "idx")
		if err != nil {
			t.Fatal(err)
		}
		store.ResetHistoryInspections()
		if mm, err := ver.VerifyEntry(audit, store, "hot"); err != nil || mm != nil {
			t.Fatalf("VerifyEntry mm=%v err=%v", mm, err)
		}
		return store.HistoryInspections(), store.HistoryLength("hot", "p")
	}

	readsShort, n1 := measure(50)
	readsLong, n2 := measure(4000)
	if n2 <= n1 {
		t.Fatalf("history did not grow: %d -> %d", n1, n2)
	}
	if readsShort != readsLong {
		t.Fatalf("history reads grew with history length: %d(n=%d) vs %d(n=%d)",
			readsShort, n1, readsLong, n2)
	}
	if readsShort > 2 {
		t.Fatalf("single-entry verify read %d records, want <= 2 constant", readsShort)
	}
	t.Logf("O(1) PROVEN: historyReads=%d constant while object history grew %d -> %d",
		readsShort, n1, n2)
}

// 只读复核幂等：同一审计 + 同一状态多次复核结论完全相同，
// 且不改变对象 LSN，也不私自标记管理器。
func TestVerifyIdempotentAndReadOnly(t *testing.T) {
	store, mgr, ver, _ := newHarness(t)
	mgr.Declare("T", "idx", "p")
	mgr.Write("T", "o1", map[string]PropertyValue{"p": "A"})
	mgr.Write("T", "o2", map[string]PropertyValue{"p": "B"})
	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatal(err)
	}
	clean, _, _ := mgr.Audit("T", "idx")

	var first string
	for i := 0; i < 3; i++ {
		s := summarize(ver.VerifyAudit(clean, store))
		if i == 0 {
			first = s
		} else if s != first {
			t.Fatalf("non-deterministic clean verification")
		}
	}

	mgr.TamperEntryForTest("T", "idx", "o1", "Z")
	dirty, _, _ := mgr.Audit("T", "idx")
	lsnBefore := store.LastLSN()
	var d0 string
	for i := 0; i < 3; i++ {
		r := ver.VerifyAudit(dirty, store)
		if r.Consistent {
			t.Fatal("dirty audit must verify inconsistent")
		}
		if i == 0 {
			d0 = summarize(r)
		} else if summarize(r) != d0 {
			t.Fatal("non-deterministic dirty verification")
		}
	}
	if store.LastLSN() != lsnBefore {
		t.Fatal("read-only verification changed object state (LSN moved)")
	}
	res, _ := mgr.Query("T", "idx", "Z")
	if res.InconsistencyDeclared {
		t.Fatal("read-only VerifyAudit must not flag the manager")
	}
}

// 被拒绝的重建/复核不得改变对象状态或既有索引内容。
func TestRejectedRequestsHaveNoEffect(t *testing.T) {
	store, mgr, ver, _ := newHarness(t)
	mgr.Write("T", "o1", map[string]PropertyValue{"p": "A"})
	before := store.LastLSN()

	if _, err := mgr.Rebuild("T", "nope"); !errors.Is(err, ErrIndexNotDeclared) {
		t.Fatalf("rebuild undeclared: %v", err)
	}
	if _, err := ver.VerifyManager(mgr, "T", "nope", true); !errors.Is(err, ErrIndexNotDeclared) {
		t.Fatalf("verify undeclared: %v", err)
	}
	if store.LastLSN() != before {
		t.Fatal("rejected requests changed object state")
	}

	mgr.Declare("T", "idx", "p")
	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatal(err)
	}
	good, _, _ := mgr.Audit("T", "idx")

	mgr.FailNextRebuild(true)
	if _, err := mgr.Rebuild("T", "idx"); !errors.Is(err, ErrIndexUnavailable) {
		t.Fatalf("failed rebuild: %v", err)
	}
	if ph, _ := mgr.Phase("T", "idx"); ph != PhaseFailed {
		t.Fatalf("phase=%s, want failed", ph)
	}
	if !good.VerifyDigest() {
		t.Fatal("failed rebuild mutated the previously sealed audit copy")
	}
	if _, err := mgr.Query("T", "idx", "A"); !errors.Is(err, ErrIndexUnavailable) {
		t.Fatalf("query after failed rebuild: %v", err)
	}
}

// 并发复核一致性：复核请求在并发写入下到达。管理器的存活审计与写入
// 在同一串行点推进，复核也在同一串行点原子采集，因此每个复核者看到
// 的都是某个自洽的全局串行点；停写后对最终状态多次复核结论完全一致，
// 且运行期所有结论都包含同一个被注入的条目级不一致。
func TestConcurrentVerifiersConsistent(t *testing.T) {
	_, mgr, ver, _ := newHarness(t)
	mgr.Declare("T", "idx", "p")
	for i := 0; i < 20; i++ {
		mgr.Write("T", fmt.Sprintf("o%d", i),
			map[string]PropertyValue{"p": []string{"A", "B", "C"}[i%3]})
	}
	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatal(err)
	}
	mgr.TamperEntryForTest("T", "idx", "o7", "TAMPERED")

	const n = 24
	type result struct {
		summary string
		hasO7   bool
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 背景持续写入同类型对象，制造写入与复核的真实交错。
	go func() {
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
				mgr.Write("T", fmt.Sprintf("bg%d", i%5),
					map[string]PropertyValue{"p": []string{"X", "Y", "Z"}[i%3]})
				i++
				time.Sleep(time.Microsecond)
			}
		}
	}()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// 只读复核（不触发管理器标记），保证复核互不影响。
			rec, _, err := mgr.Audit("T", "idx")
			if err != nil {
				t.Errorf("audit: %v", err)
				return
			}
			r := ver.VerifyAudit(rec, mgr.Store())
			s := summarize(r)
			results[idx] = result{
				summary: s,
				hasO7:   strings.Contains(s, "o7") && strings.Contains(s, "TAMPERED"),
			}
		}(i)
	}
	close(stop)
	wg.Wait()

	// 背景写入不会修复被篡改的 o7，因此每个并发复核都必须报出同一条不一致。
	for i, r := range results {
		if !r.hasO7 {
			t.Fatalf("verifier %d failed to report the injected o7/TAMPERED mismatch: %s",
				i, r.summary)
		}
	}

	// 停写、修复审计（成功重建）后，多次并发复核必须得到完全相同的结论。
	if _, err := mgr.Rebuild("T", "idx"); err != nil {
		t.Fatal(err)
	}
	final := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rec, _, err := mgr.Audit("T", "idx")
			if err != nil {
				t.Errorf("audit: %v", err)
				return
			}
			final[idx] = summarize(ver.VerifyAudit(rec, mgr.Store()))
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if final[i] != final[0] {
			t.Fatalf("post-quiesce verifiers disagreed:\n%s\nvs\n%s", final[0], final[i])
		}
	}
	if !strings.HasPrefix(final[0], "consistent=true") {
		t.Fatalf("after repair rebuild verification must be consistent: %s", final[0])
	}
}
