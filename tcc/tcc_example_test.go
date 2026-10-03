package tcc_test

import (
	"errors"
	"testing"

	"ontology/ledger"
	"ontology/tcc"
)

func newMgr(t *testing.T, ttl int64, n int, acct string, bal int64) *tcc.Manager {
	t.Helper()
	lg := ledger.New()
	if err := lg.Deposit([]byte(acct), bal); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	m, err := tcc.New(lg, ttl, n)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// 题目主例：ttl=10，a 余额 100。
func TestSpecExample(t *testing.T) {
	m := newMgr(t, 10, 100, "a", 100)
	x := []byte("x")
	a := []byte("a")
	if err := m.Try(x, []byte("1"), a, 60, 0); err != nil {
		t.Fatalf("Try br1: %v", err)
	}
	t.Logf("输入 Try(x,1,a,60,0) -> 输出 nil，判定 fz=60 到期=10；实际 fz=%d",
		m.Ledger().Frozen(a))
	if m.Ledger().Frozen(a) != 60 {
		t.Fatalf("fz=%d want 60", m.Ledger().Frozen(a))
	}
	err := m.Try(x, []byte("2"), a, 50, 1)
	t.Logf("输入 Try(x,2,a,50,1) -> 输出 %v，判定 可用40<50 ErrInsufficient", err)
	if !errors.Is(err, ledger.ErrInsufficient) {
		t.Fatalf("err=%v want ErrInsufficient", err)
	}
	if err := m.Cancel(x, []byte("3"), 2); err != nil {
		t.Fatalf("empty rollback: %v", err)
	}
	t.Logf("输入 Cancel(x,3,2) -> 输出 nil，判定记录不存在且容量未满，写 Cancelled(Empty)")
	err = m.Try(x, []byte("3"), a, 10, 3)
	t.Logf("输入 Try(x,3,a,10,3) -> 输出 %v，判定 先到取消阻止迟到预留 ErrHanging", err)
	if !errors.Is(err, ledger.ErrHanging) {
		t.Fatalf("err=%v want ErrHanging", err)
	}
	// 情形一：到期前 Confirm。
	if err := m.Confirm(x, []byte("1"), 9); err != nil {
		t.Fatalf("Confirm@9: %v", err)
	}
	t.Logf("输入 Confirm(x,1,9) -> 输出 nil，判定 9<10 未到期；bal=40 fz=0")
	if m.Ledger().Balance(a) != 40 || m.Ledger().Frozen(a) != 0 {
		t.Fatalf("bal=%d fz=%d want 40/0", m.Ledger().Balance(a), m.Ledger().Frozen(a))
	}
}

// 另一情形：恰在到期时刻 Confirm，先到期得 ErrExpired，可用额回到 100。
func TestExpiryAtDeadline(t *testing.T) {
	m := newMgr(t, 10, 100, "a", 100)
	x, a := []byte("x"), []byte("a")
	if err := m.Try(x, []byte("1"), a, 60, 0); err != nil {
		t.Fatal(err)
	}
	err := m.Confirm(x, []byte("1"), 10)
	t.Logf("输入 Confirm(x,1,10) -> 输出 %v，判定 now==到期10 先到期，ErrExpired", err)
	if !errors.Is(err, ledger.ErrExpired) {
		t.Fatalf("err=%v want ErrExpired", err)
	}
	v, err := m.Avail(a, 10)
	if err != nil || v != 100 {
		t.Fatalf("Avail=%d,%v want 100", v, err)
	}
	t.Logf("拒绝未落实到期；只读 Avail(a,10)=%d 为 now 的纯函数（虚拟释放），记录仍 Tried", v)
	if rec, ok := m.Get(x, []byte("1")); !ok || rec.State != tcc.StateTried {
		t.Fatalf("被拒绝的 Confirm 不得落实到期，rec=%+v ok=%v", rec, ok)
	}
	// 成功操作（Avail 只读不推进时钟，故 now=9 仍合法）差 1 不过期：Try 幂等成功。
	if err := m.Try(x, []byte("1"), a, 60, 9); err != nil {
		t.Fatalf("now=9 未到期，幂等 Try 应成功: %v", err)
	}
	t.Logf("差1：Try(x,1,a,60,9) 幂等成功，到期时刻不刷新，fz 仍=60")
	if m.Ledger().Frozen(a) != 60 {
		t.Fatalf("fz=%d want 60", m.Ledger().Frozen(a))
	}
}

func TestExpiryOneBefore(t *testing.T) {
	m := newMgr(t, 10, 100, "a", 100)
	x, a := []byte("x"), []byte("a")
	if err := m.Try(x, []byte("1"), a, 60, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(x, []byte("1"), 9); err != nil {
		t.Fatalf("Cancel@9: %v", err)
	}
	t.Logf("差1：now=9<到期10，Cancel 成功释放；后续 Try 被 ErrHanging")
	if err := m.Try(x, []byte("1"), a, 1, 9); !errors.Is(err, ledger.ErrHanging) {
		t.Fatalf("late Try err=%v want ErrHanging", err)
	}
}

func TestIdempotencyAndCapacity(t *testing.T) {
	m := newMgr(t, 2, 2, "a", 100)
	x, a := []byte("x"), []byte("a")
	if err := m.Try(x, []byte("1"), a, 10, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Try(x, []byte("1"), a, 10, 1); err != nil {
		t.Fatalf("幂等 Try 应成功: %v", err)
	}
	if m.Ledger().Frozen(a) != 10 {
		t.Fatalf("幂等 Try 不得重复冻结，fz=%d", m.Ledger().Frozen(a))
	}
	if err := m.Try(x, []byte("2"), a, 5, 1); err != nil {
		t.Fatal(err)
	}
	// 容量满（终态标记永久占容量），空回滚被 ErrCapacity 且不留标记。
	b, _ := m.Get(x, []byte("1"))
	if err := m.Confirm(x, []byte("1"), 1); err != nil {
		t.Fatal(err)
	}
	_ = b
	if err := m.Cancel(x, []byte("9"), 1); !errors.Is(err, ledger.ErrCapacity) {
		t.Fatalf("空回滚 err=%v want ErrCapacity", err)
	}
	if _, ok := m.Get(x, []byte("9")); ok {
		t.Fatal("ErrCapacity 时不得写空回滚标记")
	}
	t.Logf("容量满：Cancel(x,9) -> ErrCapacity，检查未写入任何记录")
	// 重复 Confirm 幂等成功。
	if err := m.Confirm(x, []byte("1"), 2); err != nil {
		t.Fatalf("重复 Confirm: %v", err)
	}
}
