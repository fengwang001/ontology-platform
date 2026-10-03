package replay

import (
	"testing"

	"ontology/code"
	"ontology/history"
)

// 对由当前代码从空历史续跑产生的完整历史 H，遍历所有崩溃点 k：
// 以 H[:k] 为初值 Run，最终历史必须恰好等于 H。
func runPrefixLoop(t *testing.T, prog code.Code) []history.Event {
	t.Helper()

	base := &history.Store{}
	r0 := NewRunner(base)
	if _, _, err := r0.Run([]byte("base"), prog); err != nil {
		t.Fatalf("build full history: %v", err)
	}
	full := base.Snapshot([]byte("base"))
	t.Logf("code=%s", codeString(prog))
	t.Logf("full H (%d events): %s", len(full), evsString(full))

	for k := 0; k <= len(full); k++ {
		store, wf := seed(t, "prefix", full[:k])
		r := NewRunner(store)
		consumed, cont, err := r.Run(wf, prog)
		if err != nil {
			t.Fatalf("k=%d: Run returned %v", k, err)
		}
		got := store.Snapshot(wf)
		t.Logf("k=%2d prefix=%s -> consumed=%d cont=%d final=%s",
			k, evsString(full[:k]), consumed, cont, evsString(got))
		mustEqualEvents(t, got, full, "prefix closure")
		if consumed != k {
			t.Fatalf("k=%d: consumed=%d, want %d", k, consumed, k)
		}
		if cont != len(full)-k {
			t.Fatalf("k=%d: cont=%d, want %d", k, cont, len(full)-k)
		}
		if c := r.comparisons(wf); c != int64(len(full)) {
			t.Fatalf("k=%d: comparisons=%d, want %d", k, c, len(full))
		}
	}
	return full
}

func TestPrefixCrashPointsV2(t *testing.T) {
	runPrefixLoop(t, v2())
}

func TestPrefixCrashPointsRepeatedPid(t *testing.T) {
	prog := code.Code{
		code.Branch([]byte("p"), []code.Item{code.Step([]byte("b2"))}, []code.Item{code.Step([]byte("b"))}),
		code.Branch([]byte("p"), []code.Item{code.Step([]byte("d2"))}, []code.Item{code.Step([]byte("d"))}),
		code.Step([]byte("e")),
	}
	runPrefixLoop(t, prog)
}

func TestPrefixCrashPointsEmptyBranches(t *testing.T) {
	prog := code.Code{
		code.Step([]byte("a")),
		code.Branch([]byte("p"), nil, nil),
		code.Step([]byte("b")),
		code.Branch([]byte("q"),
			[]code.Item{code.Step([]byte("c2"))},
			[]code.Item{code.Step([]byte("c"))}),
	}
	runPrefixLoop(t, prog)
}

// 升级语义：旧代码产生的完整历史在升级代码上重放，消费全部、零续跑、不改历史。
func TestOldHistoryReplayOnUpgradedCode(t *testing.T) {
	old := code.Code{
		code.Step([]byte("a")), code.Step([]byte("b")), code.Step([]byte("c")),
	}
	oldStore := &history.Store{}
	wf := []byte("upgrade")
	or := NewRunner(oldStore)
	if _, _, err := or.Run(wf, old); err != nil {
		t.Fatalf("old-code run: %v", err)
	}
	oldHist := oldStore.Snapshot(wf)
	t.Logf("old history: %s", evsString(oldHist))

	store, w := seed(t, "upgrade", oldHist)
	r := NewRunner(store)
	consumed, cont, err := r.Run(w, v2())
	t.Logf("upgraded code=%s; consumed=%d cont=%d err=%v",
		codeString(v2()), consumed, cont, err)
	if err != nil || consumed != len(oldHist) || cont != 0 {
		t.Fatalf("got (%d,%d,%v), want (%d,0,nil)", consumed, cont, err, len(oldHist))
	}
	mustEqualEvents(t, store.Snapshot(w), oldHist, "old history unchanged")
}
