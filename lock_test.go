package ontology

import (
	"errors"
	"testing"
)

func mustRegister(t *testing.T, m *LockManager, id, parent string) {
	t.Helper()
	if err := m.Register(id, parent); err != nil {
		t.Fatalf("Register(%q,%q) 意外失败: %v", id, parent, err)
	}
}

func mustLock(t *testing.T, m *LockManager, txn int64, node string, mode Mode) Mode {
	t.Helper()
	got, err := m.Lock(txn, node, mode)
	if err != nil {
		t.Fatalf("Lock(%d,%q,%q) 意外失败: %v", txn, node, mode, err)
	}
	return got
}

func assertHeld(t *testing.T, m *LockManager, txn int64, node string, want Mode) {
	t.Helper()
	got, has, err := m.Held(txn, node)
	if err != nil || !has || got != want {
		t.Fatalf("Held(%d,%q) = %q,%v,%v, 想要 %q", txn, node, got, has, err, want)
	}
}

// S 与 IX 合并为 SIX：同一事务在同节点先 IX 后 S（或反之）。
func TestJoinSAndIXBecomesSIX(t *testing.T) {
	m := NewLockManager()
	mustRegister(t, m, "root", "")
	mustRegister(t, m, "a", "root")

	mustLock(t, m, 1, "root", IX)
	mustLock(t, m, 1, "a", IX)
	if got := mustLock(t, m, 1, "a", S); got != SIX {
		t.Fatalf("join(IX,S) = %q, 想要 SIX", got)
	}
	assertHeld(t, m, 1, "a", SIX)

	mustRegister(t, m, "b", "root")
	mustLock(t, m, 2, "root", IX)
	mustLock(t, m, 2, "b", S)
	if got := mustLock(t, m, 2, "b", IX); got != SIX {
		t.Fatalf("join(S,IX) = %q, 想要 SIX", got)
	}
}

// S 不满足 IX 所需意向。
func TestSDoesNotSatisfyIXIntent(t *testing.T) {
	m := NewLockManager()
	mustRegister(t, m, "root", "")
	mustRegister(t, m, "a", "root")
	mustLock(t, m, 1, "root", S)

	_, err := m.Lock(1, "a", IX)
	var ie *AncestorIntentError
	if !errors.As(err, &ie) {
		t.Fatalf("期望 AncestorIntentError，得到 %v", err)
	}
	if !errors.Is(err, ErrMissingIntent) || ie.Ancestor != "root" ||
		ie.Required != IX || ie.Held != S {
		t.Fatalf("意向错误内容不符: %+v", ie)
	}
	if _, err := m.Lock(1, "a", SIX); !errors.Is(err, ErrMissingIntent) {
		t.Fatalf("SIX 也应缺 IX 意向，得到 %v", err)
	}
	if _, err := m.Lock(1, "a", X); !errors.Is(err, ErrMissingIntent) {
		t.Fatalf("X 也应缺 IX 意向，得到 %v", err)
	}
}

// 转换按 join 后的模式而非请求模式检查祖先：
// a 上持 S、祖先只有 IS 时请求 IX，join 得 SIX（需 IX 意向），被拒。
func TestConversionChecksJoinedModeNotRequestedMode(t *testing.T) {
	m := NewLockManager()
	mustRegister(t, m, "root", "")
	mustRegister(t, m, "a", "root")
	mustLock(t, m, 1, "root", IS)
	mustLock(t, m, 1, "a", S)

	_, err := m.Lock(1, "a", IX)
	var ie *AncestorIntentError
	if !errors.As(err, &ie) || ie.Ancestor != "root" || ie.Required != IX {
		t.Fatalf("期望 root 缺 IX 意向，得到 %v", err)
	}
	assertHeld(t, m, 1, "a", S)
}

// SIX 与 IS 相容而与 IX 冲突；冲突错误可取最小冲突事务。
func TestSIXCompatibilityAndConflictDetail(t *testing.T) {
	m := NewLockManager()
	mustRegister(t, m, "n", "")
	mustLock(t, m, 5, "n", SIX)

	if _, err := m.Lock(2, "n", IS); err != nil {
		t.Fatalf("SIX 应与 IS 相容: %v", err)
	}
	_, err := m.Lock(9, "n", IX)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("期望 ConflictError，得到 %v", err)
	}
	if !errors.Is(err, ErrLockConflict) || ce.ConflictTxn != 5 || ce.ConflictMode != SIX {
		t.Fatalf("冲突细节不符: %+v", ce)
	}

	mustLock(t, m, 7, "n", IS)
	_, err = m.Lock(9, "n", X)
	if !errors.As(err, &ce) || ce.ConflictTxn != 2 {
		t.Fatalf("X 应与最小冲突事务 2 冲突，得到 %+v", ce)
	}

	holders, err := m.Holders("n")
	if err != nil || len(holders) != 3 ||
		holders[0] != (Holder{2, IS}) ||
		holders[1] != (Holder{5, SIX}) ||
		holders[2] != (Holder{7, IS}) {
		t.Fatalf("Holders 顺序/内容不符: %+v, err=%v", holders, err)
	}
}

// 已持不弱于请求的模式时为无操作，不再检查祖先与冲突。
func TestWeakerRequestIsNoop(t *testing.T) {
	m := NewLockManager()
	mustRegister(t, m, "b", "")
	mustLock(t, m, 1, "b", SIX)
	mustLock(t, m, 4, "b", IS)

	for _, req := range []Mode{S, IX, IS, SIX} {
		if got := mustLock(t, m, 1, "b", req); got != SIX {
			t.Fatalf("请求 %q 应无操作仍为 SIX，得到 %q", req, got)
		}
	}
	assertHeld(t, m, 1, "b", SIX)

	mustRegister(t, m, "r", "")
	mustRegister(t, m, "c", "r")
	mustLock(t, m, 1, "r", IX)
	mustLock(t, m, 1, "c", X)
	if got := mustLock(t, m, 1, "c", S); got != X {
		t.Fatalf("X 上请求 S 应无操作，得到 %q", got)
	}
}

// Unlock 在后代仍持锁时被拒，而 ReleaseAll 一次成功。
func TestUnlockRejectedWithDescendantButReleaseAllSucceeds(t *testing.T) {
	m := NewLockManager()
	mustRegister(t, m, "root", "")
	mustRegister(t, m, "a", "root")
	mustRegister(t, m, "c", "a")
	mustLock(t, m, 1, "root", IX)
	mustLock(t, m, 1, "a", IX)
	mustLock(t, m, 1, "c", X)

	err := m.Unlock(1, "root")
	var de *DescendantLockError
	if !errors.As(err, &de) || !errors.Is(err, ErrDescendantLocked) {
		t.Fatalf("期望后代锁错误，得到 %v", err)
	}
	if de.Descendant != "a" {
		t.Fatalf("后代字段应为 id 最小的 a，得到 %+v", de)
	}
	assertHeld(t, m, 1, "root", IX)

	if err := m.Unlock(1, "c"); err != nil {
		t.Fatal(err)
	}
	if err := m.Unlock(1, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Unlock(1, "root"); err != nil {
		t.Fatalf("后代已清空，应可释放: %v", err)
	}

	mustLock(t, m, 2, "root", IX)
	mustLock(t, m, 2, "a", S)
	if err := m.ReleaseAll(2); err != nil {
		t.Fatal(err)
	}
	if _, has, err := m.Held(2, "root"); err != nil || has {
		t.Fatalf("ReleaseAll 后 root 应无锁: has=%v err=%v", has, err)
	}
	if _, has, _ := m.Held(2, "a"); has {
		t.Fatal("ReleaseAll 后 a 应无锁")
	}
	if err := m.ReleaseAll(99); err != nil {
		t.Fatalf("无锁事务 ReleaseAll 应成功: %v", err)
	}
}

// 被拒绝的请求不改变任何持锁状态。
func TestRejectedLockLeavesStateUntouched(t *testing.T) {
	m := NewLockManager()
	mustRegister(t, m, "root", "")
	mustRegister(t, m, "a", "root")
	mustLock(t, m, 1, "root", IS)
	mustLock(t, m, 1, "a", S)

	before, _ := m.Holders("a")
	if _, err := m.Lock(1, "a", IX); !errors.Is(err, ErrMissingIntent) {
		t.Fatalf("期望缺意向，得到 %v", err)
	}
	if _, err := m.Lock(0, "a", X); !errors.Is(err, ErrInvalidTxn) {
		t.Fatalf("事务号校验: %v", err)
	}
	if _, err := m.Lock(2, "a", Mode("Z")); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("模式校验: %v", err)
	}
	if _, err := m.Lock(2, "ghost", X); !errors.Is(err, ErrNodeNotExist) {
		t.Fatalf("节点校验: %v", err)
	}
	// 事务 2 先在 root 取 IX（与 1 的 IS 相容），再在 a 请 X：
	// 祖先意向满足后，X 与事务 1 的 S 冲突。
	mustLock(t, m, 2, "root", IX)
	if _, err := m.Lock(2, "a", X); !errors.Is(err, ErrLockConflict) {
		t.Fatalf("冲突校验: %v", err)
	}
	after, _ := m.Holders("a")
	if len(after) != len(before) || after[0] != before[0] {
		t.Fatalf("拒绝后状态被改变: before=%+v after=%+v", before, after)
	}
	// 冲突拒绝同样不改祖先状态。
	assertHeld(t, m, 2, "root", IX)

	mustRegister(t, m, "d", "root")
	mustLock(t, m, 1, "d", S)
	if err := m.Unlock(1, "root"); !errors.Is(err, ErrDescendantLocked) {
		t.Fatalf("期望后代锁错误: %v", err)
	}
	assertHeld(t, m, 1, "root", IS)
}

// 错误优先级与查询错误原因。
func TestErrorOrderingAndQueries(t *testing.T) {
	m := NewLockManager()
	mustRegister(t, m, "root", "")

	if err := m.Register("", "x"); !errors.Is(err, ErrEmptyNodeID) {
		t.Fatal(err)
	}
	if err := m.Register("root", ""); !errors.Is(err, ErrNodeExists) {
		t.Fatal(err)
	}
	if err := m.Register("a", "missing"); !errors.Is(err, ErrParentNotExist) {
		t.Fatal(err)
	}

	if _, err := m.Lock(-1, "root", X); !errors.Is(err, ErrInvalidTxn) {
		t.Fatal(err)
	}
	if _, err := m.Lock(1, "root", Mode("Q")); !errors.Is(err, ErrInvalidMode) {
		t.Fatal(err)
	}
	if _, err := m.Lock(1, "nope", X); !errors.Is(err, ErrNodeNotExist) {
		t.Fatal(err)
	}

	if err := m.Unlock(0, "root"); !errors.Is(err, ErrInvalidTxn) {
		t.Fatal(err)
	}
	if err := m.Unlock(1, "nope"); !errors.Is(err, ErrNodeNotExist) {
		t.Fatal(err)
	}
	if err := m.Unlock(1, "root"); !errors.Is(err, ErrLockNotHeld) {
		t.Fatal(err)
	}
	if err := m.ReleaseAll(0); !errors.Is(err, ErrInvalidTxn) {
		t.Fatal(err)
	}

	if _, _, err := m.Held(0, "root"); !errors.Is(err, ErrInvalidTxn) {
		t.Fatal(err)
	}
	if _, _, err := m.Held(1, "nope"); !errors.Is(err, ErrNodeNotExist) {
		t.Fatal(err)
	}
	if _, err := m.Holders("nope"); !errors.Is(err, ErrNodeNotExist) {
		t.Fatal(err)
	}

	if mode, has, err := m.Held(1, "root"); err != nil || has || mode != "" {
		t.Fatalf("未持有时 Held 应为空标志: %q %v %v", mode, has, err)
	}
	if holders, err := m.Holders("root"); err != nil || len(holders) != 0 {
		t.Fatalf("空节点 Holders 应为空序列: %+v %v", holders, err)
	}
}
