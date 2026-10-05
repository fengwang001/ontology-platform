package holding

import (
	"errors"
	"testing"
)

type op struct {
	run  func(b *Book) error
	want error // 期望的哨兵错误；nil 表示成功
}

func runOps(t *testing.T, ops []op) *Book {
	t.Helper()
	b := NewBook()
	for i, o := range ops {
		err := o.run(b)
		if o.want == nil {
			if err != nil {
				t.Fatalf("op %d: want nil, got %v", i, err)
			}
			continue
		}
		if !errors.Is(err, o.want) {
			t.Fatalf("op %d: want %v, got %v", i, o.want, err)
		}
	}
	return b
}

func TestDeposit(t *testing.T) {
	b := runOps(t, []op{
		{func(b *Book) error { return b.Deposit("", 1) }, ErrInvalidParam},
		{func(b *Book) error { return b.Deposit("A", 0) }, ErrInvalidParam},
		{func(b *Book) error { return b.Deposit("A", -5) }, ErrInvalidParam},
		{func(b *Book) error { return b.Deposit("A", MaxDeposit+1) }, ErrInvalidParam},
		{func(b *Book) error { return b.Deposit("A", 100) }, nil},
		{func(b *Book) error { return b.Deposit("A", MaxDeposit) }, nil},
	})
	avail, frozen, ok := b.Cash("A")
	if !ok || avail != 100+MaxDeposit || frozen != 0 {
		t.Fatalf("cash = %d,%d,%v", avail, frozen, ok)
	}
	if _, _, ok := b.Cash("B"); ok {
		t.Fatal("B should not exist")
	}
}

func TestTrade(t *testing.T) {
	b := runOps(t, []op{
		// 参数非法优先于不存在
		{func(b *Book) error { return b.Trade("X", "S", 0) }, ErrInvalidParam},
		{func(b *Book) error { return b.Trade("", "S", 1) }, ErrInvalidParam},
		{func(b *Book) error { return b.Trade("X", "", 1) }, ErrInvalidParam},
		{func(b *Book) error { return b.Trade("X", "S", MaxDelta+1) }, ErrInvalidParam},
		{func(b *Book) error { return b.Trade("X", "S", -MaxDelta-1) }, ErrInvalidParam},
		// 卖出：账户不存在
		{func(b *Book) error { return b.Trade("X", "S", -1) }, ErrNotExist},
		// 买入建立账户
		{func(b *Book) error { return b.Trade("X", "S", 100) }, nil},
		// 卖出超过可用
		{func(b *Book) error { return b.Trade("X", "S", -101) }, ErrInsufficient},
		// 卖出未持有的标的
		{func(b *Book) error { return b.Trade("X", "T", -1) }, ErrInsufficient},
		{func(b *Book) error { return b.Trade("X", "S", -40) }, nil},
	})
	q, f, ok := b.Position("X", "S")
	if !ok || q != 60 || f != 0 {
		t.Fatalf("pos = %d,%d,%v", q, f, ok)
	}
}

func TestFreezeUnfreeze(t *testing.T) {
	b := runOps(t, []op{
		{func(b *Book) error { return b.Freeze("X", "S", 0) }, ErrInvalidParam},
		{func(b *Book) error { return b.Freeze("X", "S", -1) }, ErrInvalidParam},
		{func(b *Book) error { return b.Unfreeze("", "S", 1) }, ErrInvalidParam},
		// 账户不存在
		{func(b *Book) error { return b.Freeze("X", "S", 1) }, ErrNotExist},
		{func(b *Book) error { return b.Unfreeze("X", "S", 1) }, ErrNotExist},
		{func(b *Book) error { return b.Trade("X", "S", 100) }, nil},
		// 冻结超过可用
		{func(b *Book) error { return b.Freeze("X", "S", 101) }, ErrInsufficient},
		{func(b *Book) error { return b.Freeze("X", "S", 70) }, nil},
		{func(b *Book) error { return b.Freeze("X", "S", 31) }, ErrInsufficient},
		// 解冻超过冻结
		{func(b *Book) error { return b.Unfreeze("X", "S", 71) }, ErrInsufficient},
		{func(b *Book) error { return b.Unfreeze("X", "S", 20) }, nil},
		// 冻结后可用不足，卖出受限
		{func(b *Book) error { return b.Trade("X", "S", -51) }, ErrInsufficient},
		{func(b *Book) error { return b.Trade("X", "S", -50) }, nil},
	})
	q, f, ok := b.Position("X", "S")
	if !ok || q != 50 || f != 50 {
		t.Fatalf("pos = %d,%d,%v", q, f, ok)
	}
}

// 不变量 0<=f<=q 与快照只含 q>0 账户。
func TestInvariantAndSnapshot(t *testing.T) {
	b := NewBook()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.Trade("A", "S", 1005))
	must(b.Freeze("A", "S", 333))
	must(b.Trade("B", "S", 10))
	must(b.Trade("B", "S", -10)) // B 卖光，快照不应包含
	must(b.Trade("C", "T", 7))   // C 只持有其他标的

	snap := b.SnapshotSymbol("S")
	if len(snap) != 1 {
		t.Fatalf("snapshot size = %d, want 1", len(snap))
	}
	if got := snap["A"]; got.Q != 1005 || got.F != 333 {
		t.Fatalf("snapshot A = %+v", got)
	}

	// 任意操作后不变量保持
	for acct, st := range b.Dump() {
		for sym, p := range st.Positions {
			if p.F < 0 || p.F > p.Q {
				t.Fatalf("invariant violated: %s %s q=%d f=%d", acct, sym, p.Q, p.F)
			}
		}
	}
}

func TestCredit(t *testing.T) {
	b := NewBook()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(b.Trade("A", "S", 100))
	must(b.Freeze("A", "S", 30))
	// 除权入账：送股 10（冻结 3）、现金 50（冻结 15）、碎股折现 4
	b.Credit("A", "S", 10, 3, 50-15+4, 15)
	q, f, _ := b.Position("A", "S")
	if q != 110 || f != 33 {
		t.Fatalf("pos = %d,%d", q, f)
	}
	avail, frozen, _ := b.Cash("A")
	if avail != 39 || frozen != 15 {
		t.Fatalf("cash = %d,%d", avail, frozen)
	}
	// 对无持仓账户入账会建立持仓
	b.Credit("D", "S", 1, 0, 2, 0)
	if q, _, ok := b.Position("D", "S"); !ok || q != 1 {
		t.Fatalf("D pos q=%d ok=%v", q, ok)
	}
}
