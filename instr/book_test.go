package instr

import (
	"errors"
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func TestNewPanics(t *testing.T) {
	cases := []struct {
		u, rs, rb int64
		a         int
	}{
		{0, 1, 1, 1}, {1_000_001, 1, 1, 1},
		{1, -1, 1, 1}, {1, 10_001, 1, 1},
		{1, 1, -1, 1}, {1, 1, 10_001, 1},
		{1, 1, 1, 0}, {1, 1, 1, 101},
	}
	for i, c := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("case %d expected panic", i)
				}
			}()
			New(c.u, c.rs, c.rb, c.a)
		}()
	}
}

// TestInstructValidation 覆盖 Instruct 的参数非法各分支。
func TestInstructValidation(t *testing.T) {
	setup := func() *Book {
		b := New(100, 10, 5, 2)
		mustOK(t, b.SetPrice(1, "AAA", 11), "setprice")
		return b
	}
	cases := []struct {
		name                   string
		day                    int
		id, seller, buyer, sym string
		qty, amount            int64
		sd                     int
		want                   error
	}{
		{"ok", 1, "i1", "S", "B", "AAA", 1000, 10005, 2, nil},
		{"qty not multiple of U", 1, "i2", "S", "B", "AAA", 150, 100, 2, ErrInvalid},
		{"qty zero", 1, "i3", "S", "B", "AAA", 0, 100, 2, ErrInvalid},
		{"qty over 1e9", 1, "i4", "S", "B", "AAA", 1_000_000_100, 100, 2, ErrInvalid},
		{"amount zero", 1, "i5", "S", "B", "AAA", 100, 0, 2, ErrInvalid},
		{"amount over 1e15", 1, "i6", "S", "B", "AAA", 100, 1_000_000_000_000_001, 2, ErrInvalid},
		{"sd not > day", 1, "i7", "S", "B", "AAA", 100, 100, 1, ErrInvalid},
		{"seller==buyer", 1, "i8", "S", "S", "AAA", 100, 100, 2, ErrInvalid},
		{"empty id", 1, "", "S", "B", "AAA", 100, 100, 2, ErrInvalid},
		{"empty seller", 1, "i9", "", "B", "AAA", 100, 100, 2, ErrInvalid},
		{"day negative", -1, "i10", "S", "B", "AAA", 100, 100, 2, ErrInvalid},
		{"day over 1e6", 1_000_001, "i11", "S", "B", "AAA", 100, 100, 1_000_002, ErrInvalid},
		{"no price for symbol", 1, "i12", "S", "B", "ZZZ", 100, 100, 2, ErrNoPrice},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := setup()
			err := b.Instruct(c.day, c.id, c.seller, c.buyer, c.sym, c.qty, c.amount, c.sd)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
}

func TestCreditValidationAndBounds(t *testing.T) {
	b := New(1, 0, 0, 1)
	if !errors.Is(b.Credit(0, "S", "X", 0), ErrInvalid) {
		t.Fatal("qty 0 must be invalid")
	}
	if !errors.Is(b.Credit(0, "S", "X", 1_000_000_000_001), ErrInvalid) {
		t.Fatal("qty >1e12 must be invalid")
	}
	if !errors.Is(b.Credit(0, "", "X", 1), ErrInvalid) {
		t.Fatal("empty acct invalid")
	}
	if !errors.Is(b.CreditCash(0, "S", 0), ErrInvalid) {
		t.Fatal("cash 0 invalid")
	}
	if !errors.Is(b.SetPrice(0, "X", 0), ErrInvalid) ||
		!errors.Is(b.SetPrice(0, "X", 1_000_001), ErrInvalid) {
		t.Fatal("price out of [1,1e6] invalid")
	}
	mustOK(t, b.Credit(5, "S", "X", 10), "credit")
	if !errors.Is(b.Credit(4, "S", "X", 10), ErrRollback) {
		t.Fatal("day rollback must be rejected")
	}
	if got := b.Holdings("S", "X"); got != 10 {
		t.Fatalf("rejected rollback must not change holdings, got %d", got)
	}
	if b.MaxDay() != 5 {
		t.Fatalf("maxday=%d want 5", b.MaxDay())
	}
}

// TestRejectOrder 验证拒绝次序：参数非法 > 日期回退 > 重复/不存在/无现价 > 状态不符。
func TestRejectOrder(t *testing.T) {
	b := New(100, 10, 5, 2)
	mustOK(t, b.SetPrice(1, "AAA", 11), "price")
	mustOK(t, b.Instruct(1, "i1", "S", "B", "AAA", 100, 100, 5), "instruct")

	// Instruct：重复 id 与无现价同时成立时，参数都合法、日期未回退，
	// 但"重复"优先于"无现价"不可能同时（重复指令的 sym 有现价）；
	// 这里验证日期回退优先于重复。
	if !errors.Is(b.Instruct(0, "i1", "S", "B", "AAA", 100, 100, 2), ErrRollback) {
		t.Fatal("rollback must beat duplicate")
	}
	// 参数非法优先于日期回退。
	if !errors.Is(b.Instruct(0, "i9", "S", "B", "AAA", 150, 100, -1), ErrInvalid) {
		t.Fatal("invalid must beat rollback")
	}
	// 重复 id 报 ErrDuplicate。
	if !errors.Is(b.Instruct(2, "i1", "S", "B", "AAA", 100, 100, 3), ErrDuplicate) {
		t.Fatal("duplicate id")
	}

	// Cancel：不存在。
	if !errors.Is(b.Cancel(2, "nope"), ErrNotFound) {
		t.Fatal("cancel missing id")
	}
	// Cancel 参数非法优先。
	if !errors.Is(b.Cancel(-1, "i1"), ErrInvalid) {
		t.Fatal("cancel invalid day")
	}
	// Cancel 日期回退优先于不存在。
	if !errors.Is(b.Cancel(0, "nope"), ErrRollback) {
		t.Fatal("cancel rollback beats not found")
	}
	// day<sd 且未了结：可撤。
	mustOK(t, b.Cancel(4, "i1"), "cancel boundary sd-1")
	// 已了结：状态不符。
	if !errors.Is(b.Cancel(4, "i1"), ErrState) {
		t.Fatal("cancel cancelled order must be state error")
	}

	// Cancel 日期边界：day==sd 不允许（已到期）。
	mustOK(t, b.Instruct(4, "i2", "S", "B", "AAA", 100, 100, 5), "i2")
	if !errors.Is(b.Cancel(5, "i2"), ErrState) {
		t.Fatal("cancel at day==sd must be state error")
	}
}

func TestRejectedOpChangesNothing(t *testing.T) {
	b := New(100, 10, 5, 2)
	mustOK(t, b.SetPrice(1, "AAA", 11), "price")
	mustOK(t, b.Instruct(1, "i1", "S", "B", "AAA", 100, 100, 3), "instr")
	// 无现价拒绝后 maxDay 不应被推进。
	_ = b.Instruct(2, "i2", "S", "B", "ZZZ", 100, 100, 3)
	if b.MaxDay() != 1 {
		t.Fatalf("rejected instruct must not advance maxDay, got %d", b.MaxDay())
	}
	if _, ok := b.Get("i2"); ok {
		t.Fatal("rejected instruction must not exist")
	}
}

func TestSeqByAcceptOrder(t *testing.T) {
	b := New(100, 0, 0, 10)
	mustOK(t, b.SetPrice(0, "X", 1), "p")
	for _, id := range []string{"a", "b", "c"} {
		mustOK(t, b.Instruct(0, id, "S", "B", "X", 100, 100, 5), id)
	}
	want := map[string]int{"a": 1, "b": 2, "c": 3}
	for id, s := range want {
		ins, _ := b.Get(id)
		if ins.Seq != s {
			t.Fatalf("%s seq=%d want %d", id, ins.Seq, s)
		}
	}
}

// TestConcurrentConservation 并发入账：持券/现金之和只随入账增加且不为负。
func TestConcurrentConservation(t *testing.T) {
	b := New(1, 0, 0, 10)
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		acct := "A"
		go func() { defer wg.Done(); _ = b.Credit(0, acct, "X", 1) }()
		go func() { defer wg.Done(); _ = b.CreditCash(0, acct, 1) }()
	}
	wg.Wait()
	if got := b.Holdings("A", "X"); got != n {
		t.Fatalf("holdings=%d want %d", got, n)
	}
	if got := b.Cash("A"); got != n {
		t.Fatalf("cash=%d want %d", got, n)
	}
}
