package ontology

import "testing"

// TestExecutorRecovery 基于同一审计序列重新打开执行器，状态完全重建，
// 且后续追加序号连续、结果与单进程一致。
func TestExecutorRecovery(t *testing.T) {
	store := NewAuditStore()
	store.Seed("A", "init")
	exec, err := NewExecutor(store)
	if err != nil {
		t.Fatal(err)
	}
	r1, _ := exec.ExecuteAction("a1", map[string]string{"A": "v1"}, false)
	_, _ = exec.ExecuteAction("rb", map[string]string{"A": "x"}, true)
	_, _ = exec.ExecuteAction("a2", map[string]string{"A": "v2"}, false)
	_, _ = exec.Correct("fix", r1.Seq, map[string]string{"A": "v1-fixed"})

	// 重新打开：从不可篡改序列恢复全部内存状态。
	reopened, err := NewExecutor(store)
	if err != nil {
		t.Fatalf("重开失败: %v", err)
	}
	want := NewReplayer(store).StateAt(store.Len())
	got := reopened.StateAt(store.Len())
	mustState(t, got, want, "重开后状态一致")

	cont, err := reopened.ExecuteAction("after-reopen", map[string]string{"A": "v3"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if cont.Seq != store.Len() || cont.Seq != 5 {
		t.Fatalf("重开后追加序号须连续，seq=%d len=%d", cont.Seq, store.Len())
	}
	t.Logf("依据: 重开恢复状态=%s，随后追加得到连续 seq=%d", stateString(got), cont.Seq)
}

// TestIndependentTypeSequences 不同对象类型（不同 Store）的序号空间
// 各自独立、分别从 1 严格递增，互不影响。
func TestIndependentTypeSequences(t *testing.T) {
	mk := func(seed string) (*AuditStore, *Executor) {
		s := NewAuditStore()
		s.Seed("I", seed)
		e, err := NewExecutor(s)
		if err != nil {
			t.Fatal(err)
		}
		return s, e
	}
	s1, e1 := mk("x")
	s2, e2 := mk("y")
	for i := 0; i < 3; i++ {
		if _, err := e1.ExecuteAction("t1", map[string]string{"I": "a"}, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e2.ExecuteAction("t2", map[string]string{"I": "b"}, false); err != nil {
		t.Fatal(err)
	}
	if s1.Len() != 3 || s2.Len() != 1 {
		t.Fatalf("两类序号空间应独立: len1=%d len2=%d", s1.Len(), s2.Len())
	}
	r, _ := s2.Get(1)
	if r.Seq != 1 || r.Changes[0].Before != "y" {
		t.Fatalf("第二类应从 seq1、自有初始值开始: %+v", r)
	}
	t.Logf("依据: 类型1 Len=%d、类型2 Len=%d 且类型2 序号从1开始，相互独立", s1.Len(), s2.Len())
}
