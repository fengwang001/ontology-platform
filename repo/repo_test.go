package repo

import (
	"errors"
	"sync"
	"testing"

	"ontology/pledge"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustIs(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err=%v, want %v", err, want)
	}
}

func acctOf(t *testing.T, e *Engine, acct string) *pledge.Account {
	t.Helper()
	a, ok := e.lib.Get(acct)
	if !ok {
		t.Fatalf("账户 %s 不存在", acct)
	}
	return a
}

// setupExample 搭建题面示例：债券 A(90,99)、B(75,98)，
// 账户入库 A 105 张、B 50 张，可用余 B 2 张。
func setupExample(t *testing.T) *Engine {
	t.Helper()
	e := New()
	mustOK(t, e.AddBond(0, []byte("A"), 90, 99))
	mustOK(t, e.AddBond(0, []byte("B"), 75, 98))
	mustOK(t, e.Credit(0, []byte("acct"), []byte("A"), 105))
	mustOK(t, e.Credit(0, []byte("acct"), []byte("B"), 52))
	mustOK(t, e.PledgeIn(0, []byte("acct"), []byte("A"), 105))
	mustOK(t, e.PledgeIn(0, []byte("acct"), []byte("B"), 50))
	return e
}

// 逐券向下取整后再求和：floor(94.5)+floor(37.5)=94+37=131，而非合并取整 132。
func TestPerBondFloor(t *testing.T) {
	e := setupExample(t)
	a := acctOf(t, e, "acct")
	if a.Cap != 131 {
		t.Fatalf("Cap=%d, want 131（逐券取整）", a.Cap)
	}
}

// 占用向上取整 ceil(130.5)=131，额度取等通过；利息向上取整 ceil(6.256)=7。
func TestRepoUseCeilEqualityAndInterest(t *testing.T) {
	e := setupExample(t)
	mustOK(t, e.Repo(0, []byte("r1"), []byte("acct"), 13050, 7, 250))
	a := acctOf(t, e, "acct")
	if a.Use != 131 {
		t.Fatalf("Use=%d, want 131", a.Use)
	}
	if a.Cap < a.Use {
		t.Fatalf("接受 Repo 后 Cap=%d < Use=%d", a.Cap, a.Use)
	}
	if a.Cash != 13050 {
		t.Fatalf("Cash=%d, want 13050", a.Cash)
	}
	info, ok := e.RepoInfo([]byte("r1"))
	if !ok {
		t.Fatal("r1 不存在")
	}
	if info.Repay != 13057 || info.Due != 7 || info.Status != Outstanding {
		t.Fatalf("info=%+v, want Repay=13057 Due=7 Outstanding", info)
	}
	// 再借 1 元占用即超额度。
	mustIs(t, e.Repo(0, []byte("r2"), []byte("acct"), 1, 1, 0), ErrInsufficientCap)
}

// 出库后额度 130<131，报标准券不足。
func TestPledgeOutInsufficientCap(t *testing.T) {
	e := setupExample(t)
	mustOK(t, e.Repo(0, []byte("r1"), []byte("acct"), 13050, 7, 250))
	mustIs(t, e.PledgeOut(0, []byte("acct"), []byte("B"), 1), ErrInsufficientCap)
	a := acctOf(t, e, "acct")
	if a.Pos["B"].Pledged != 50 || a.Cap != 131 {
		t.Fatalf("拒绝后状态被改：Pledged=%d Cap=%d", a.Pos["B"].Pledged, a.Cap)
	}
}

// 折算率下调致欠库；欠库时出零折算券仍报欠库；PledgeIn 补库解除。
func TestDeficitLifecycle(t *testing.T) {
	e := setupExample(t)
	mustOK(t, e.Repo(0, []byte("r1"), []byte("acct"), 13050, 7, 250))
	mustOK(t, e.AddBond(0, []byte("Z"), 0, 100))
	mustOK(t, e.Credit(0, []byte("acct"), []byte("Z"), 10))
	mustOK(t, e.SetRate(0, []byte("A"), 89))
	a := acctOf(t, e, "acct")
	if a.Cap != 130 || a.Use != 131 {
		t.Fatalf("Cap=%d Use=%d, want 130/131", a.Cap, a.Use)
	}
	ds := e.Deficits()
	if len(ds) != 1 || string(ds[0].Acct) != "acct" || ds[0].Gap != 1 {
		t.Fatalf("Deficits=%+v, want [{acct 1}]", ds)
	}
	// 欠库时 PledgeIn 不受限制。
	mustOK(t, e.PledgeIn(0, []byte("acct"), []byte("Z"), 10))
	// 欠库时出零折算券（不降低额度）仍报欠库。
	mustIs(t, e.PledgeOut(0, []byte("acct"), []byte("Z"), 1), ErrDeficit)
	mustIs(t, e.PledgeOut(0, []byte("acct"), []byte("B"), 1), ErrDeficit)
	mustIs(t, e.Repo(0, []byte("r2"), []byte("acct"), 100, 1, 0), ErrDeficit)
	// 补库：floor(52*75/100)=39，Cap=93+39=132，解除。
	mustOK(t, e.PledgeIn(0, []byte("acct"), []byte("B"), 2))
	if a.Cap != 132 {
		t.Fatalf("Cap=%d, want 132", a.Cap)
	}
	if ds := e.Deficits(); len(ds) != 0 {
		t.Fatalf("Deficits=%+v, want 空", ds)
	}
	mustOK(t, e.PledgeOut(0, []byte("acct"), []byte("Z"), 1))
}

// 题面续例：到期日恰等当日，入口结算由一笔被拒（可用不足）的 PledgeIn 触发，
// 结算不因拒绝回滚；违约处置余款退现金。
func TestDefaultDisposalKeepsSettlementOnRejection(t *testing.T) {
	e := setupExample(t)
	mustOK(t, e.Repo(0, []byte("r1"), []byte("acct"), 13050, 7, 250))
	mustOK(t, e.SetRate(0, []byte("A"), 89))
	mustOK(t, e.PledgeIn(0, []byte("acct"), []byte("B"), 2))
	// 第 7 日：可用 B 为 0，PledgeIn 被拒，但结算已发生。
	mustIs(t, e.PledgeIn(7, []byte("acct"), []byte("B"), 1), ErrInsufficientAvail)
	info, _ := e.RepoInfo([]byte("r1"))
	if info.Status != Defaulted || info.Repay != 13057 || info.BadDebt != 0 {
		t.Fatalf("info=%+v, want Defaulted Repay=13057 BadDebt=0", info)
	}
	a := acctOf(t, e, "acct")
	// A 卖 105 张得 10395，未偿 2662；B 卖 28 张得 2744，余 82 退现金。
	if a.Cash != 13132 {
		t.Fatalf("Cash=%d, want 13132", a.Cash)
	}
	if a.Pos["A"].Pledged != 0 || a.Pos["B"].Pledged != 24 {
		t.Fatalf("Pledged A=%d B=%d, want 0/24", a.Pos["A"].Pledged, a.Pos["B"].Pledged)
	}
	if a.Cap != 18 || a.Use != 0 {
		t.Fatalf("Cap=%d Use=%d, want 18/0", a.Cap, a.Use)
	}
	if ds := e.Deficits(); len(ds) != 0 {
		t.Fatalf("Deficits=%+v, want 空", ds)
	}
}

// 拒绝按次序只报第一个：参数非法 > 日期回退 > 不存在/重复 > 欠库 >
// 可用不足/库存不足 > 标准券不足。
func TestRejectionOrder(t *testing.T) {
	plain := func() *Engine { // 无欠库，在库 A 10 张，占用顶满
		e := New()
		mustOK(t, e.AddBond(0, []byte("A"), 100, 100))
		mustOK(t, e.Credit(0, []byte("acct"), []byte("A"), 10))
		mustOK(t, e.PledgeIn(0, []byte("acct"), []byte("A"), 10))
		mustOK(t, e.Repo(0, []byte("r1"), []byte("acct"), 1000, 10, 0))
		return e
	}
	deficit := func() *Engine { // SetRate 下调致欠库
		e := New()
		mustOK(t, e.AddBond(0, []byte("A"), 100, 100))
		mustOK(t, e.Credit(0, []byte("acct"), []byte("A"), 20))
		mustOK(t, e.PledgeIn(0, []byte("acct"), []byte("A"), 20))
		mustOK(t, e.Repo(0, []byte("r1"), []byte("acct"), 2000, 10, 0))
		mustOK(t, e.SetRate(0, []byte("A"), 50))
		return e
	}
	day5 := func() *Engine { // 已推进到第 5 日
		e := plain()
		mustOK(t, e.CreditCash(5, []byte("acct"), 1))
		return e
	}
	cases := []struct {
		name  string
		setup func() *Engine
		op    func(e *Engine) error
		want  error
	}{
		{"空债券代码", plain, func(e *Engine) error { return e.AddBond(0, nil, 90, 1) }, ErrParam},
		{"折算率越界", plain, func(e *Engine) error { return e.AddBond(0, []byte("B"), 151, 1) }, ErrParam},
		{"净价越界", plain, func(e *Engine) error { return e.AddBond(0, []byte("B"), 90, 0) }, ErrParam},
		{"数量为0", plain, func(e *Engine) error { return e.Credit(0, []byte("a"), []byte("A"), 0) }, ErrParam},
		{"金额超限", plain, func(e *Engine) error { return e.CreditCash(0, []byte("a"), 1_000_000_000_001) }, ErrParam},
		{"期限为0", plain, func(e *Engine) error { return e.Repo(0, []byte("x"), []byte("acct"), 1, 0, 0) }, ErrParam},
		{"期限越界", plain, func(e *Engine) error { return e.Repo(0, []byte("x"), []byte("acct"), 1, 366, 0) }, ErrParam},
		{"利率越界", plain, func(e *Engine) error { return e.Repo(0, []byte("x"), []byte("acct"), 1, 1, 10001) }, ErrParam},
		{"日期为负", plain, func(e *Engine) error { return e.CreditCash(-1, []byte("a"), 1) }, ErrParam},
		{"日期超限", plain, func(e *Engine) error { return e.CreditCash(1_000_001, []byte("a"), 1) }, ErrParam},
		{"参数非法优先于日期回退", day5, func(e *Engine) error { return e.PledgeOut(3, []byte("acct"), []byte("A"), 0) }, ErrParam},
		{"日期回退优先于不存在", day5, func(e *Engine) error { return e.PledgeOut(3, []byte("acct"), []byte("NOPE"), 1) }, ErrDayRollback},
		{"不存在优先于欠库", deficit, func(e *Engine) error { return e.PledgeOut(0, []byte("acct"), []byte("NOPE"), 1) }, ErrNotExist},
		{"账户不存在", plain, func(e *Engine) error { return e.Repo(0, []byte("x"), []byte("ghost"), 1, 1, 0) }, ErrNotExist},
		{"债券不存在", plain, func(e *Engine) error { return e.Credit(0, []byte("a"), []byte("NOPE"), 1) }, ErrNotExist},
		{"重复债券", plain, func(e *Engine) error { return e.AddBond(0, []byte("A"), 90, 1) }, ErrDuplicate},
		{"重复回购编号", plain, func(e *Engine) error { return e.Repo(0, []byte("r1"), []byte("acct"), 1, 1, 0) }, ErrDuplicate},
		{"欠库优先于库存不足", deficit, func(e *Engine) error { return e.PledgeOut(0, []byte("acct"), []byte("A"), 100) }, ErrDeficit},
		{"欠库时Repo被拒", deficit, func(e *Engine) error { return e.Repo(0, []byte("r2"), []byte("acct"), 100, 1, 0) }, ErrDeficit},
		{"库存不足优先于标准券不足", plain, func(e *Engine) error { return e.PledgeOut(0, []byte("acct"), []byte("A"), 11) }, ErrInsufficientStock},
		{"可用不足", plain, func(e *Engine) error { return e.PledgeIn(0, []byte("acct"), []byte("A"), 1) }, ErrInsufficientAvail},
		{"标准券不足", plain, func(e *Engine) error { return e.PledgeOut(0, []byte("acct"), []byte("A"), 1) }, ErrInsufficientCap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustIs(t, tc.op(tc.setup()), tc.want)
		})
	}
}

// 并发调用等价于某个串行顺序：无数据竞争，Cap 恒等于按定义重算的值。
func TestConcurrency(t *testing.T) {
	e := New()
	mustOK(t, e.AddBond(0, []byte("A"), 90, 100))
	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			acct := []byte{byte('a' + w)}
			id := []byte{byte('0' + w)}
			for i := 0; i < 200; i++ {
				day := int64(i)
				_ = e.Credit(day, acct, []byte("A"), 10)
				_ = e.PledgeIn(day, acct, []byte("A"), 10)
				_ = e.Repo(day, append(append([]byte{}, id...), byte(i%256), byte(i/256)), acct, 100, 5, 10)
				_ = e.PledgeOut(day, acct, []byte("A"), 1)
				_ = e.CreditCash(day, acct, 100)
				_ = e.Deficits()
			}
		}(w)
	}
	wg.Wait()
	for w := 0; w < workers; w++ {
		a, ok := e.lib.Get(string([]byte{byte('a' + w)}))
		if !ok {
			continue
		}
		want := a.Pos["A"].Pledged * 90 / 100
		if a.Cap != want {
			t.Fatalf("worker %d: Cap=%d, 重算值=%d", w, a.Cap, want)
		}
	}
}

// 现金足额时到期购回：扣款、释放占用，状态已购回。
func TestRedeemAtMaturity(t *testing.T) {
	e := setupExample(t)
	mustOK(t, e.Repo(0, []byte("r1"), []byte("acct"), 13050, 7, 250))
	mustOK(t, e.CreditCash(5, []byte("acct"), 7))
	mustOK(t, e.CreditCash(7, []byte("acct"), 1))
	info, _ := e.RepoInfo([]byte("r1"))
	if info.Status != Redeemed {
		t.Fatalf("Status=%v, want Redeemed", info.Status)
	}
	a := acctOf(t, e, "acct")
	if a.Cash != 1 || a.Use != 0 || a.Cap != 131 {
		t.Fatalf("Cash=%d Use=%d Cap=%d, want 1/0/131", a.Cash, a.Use, a.Cap)
	}
}

// 多笔到期按（到期日，被接受序号）序处理：先被接受的不先结算。
func TestMaturityOrderByDueThenSeq(t *testing.T) {
	e := New()
	mustOK(t, e.AddBond(0, []byte("A"), 100, 100))
	mustOK(t, e.Credit(0, []byte("acct"), []byte("A"), 1000))
	mustOK(t, e.PledgeIn(0, []byte("acct"), []byte("A"), 1000))
	// r1 先被接受但到期日晚；r2 到期日早，先结算。
	mustOK(t, e.Repo(0, []byte("r1"), []byte("acct"), 50000, 5, 0))
	mustOK(t, e.Repo(0, []byte("r2"), []byte("acct"), 40000, 3, 10000))
	mustOK(t, e.CreditCash(10, []byte("acct"), 1))
	// r2 应还 40329 先扣：90001-40329=49672；r1 应还 50000 现金不足，违约。
	info1, _ := e.RepoInfo([]byte("r1"))
	info2, _ := e.RepoInfo([]byte("r2"))
	if info2.Status != Redeemed {
		t.Fatalf("r2 Status=%v, want Redeemed", info2.Status)
	}
	if info1.Status != Defaulted {
		t.Fatalf("r1 Status=%v, want Defaulted", info1.Status)
	}
	a := acctOf(t, e, "acct")
	// r1 违约处置：卖 A 500 张得 50000 恰好偿清，现金不变。
	if a.Cash != 49672 || a.Pos["A"].Pledged != 500 {
		t.Fatalf("Cash=%d Pledged=%d, want 49672/500", a.Cash, a.Pos["A"].Pledged)
	}
}

// 同日到期按被接受序号；前一笔违约处置的退款使后一笔得以购回。
func TestMaturitySameDaySeqAndRefundChain(t *testing.T) {
	e := New()
	mustOK(t, e.AddBond(0, []byte("A"), 100, 99))
	mustOK(t, e.Credit(0, []byte("acct"), []byte("A"), 300))
	mustOK(t, e.PledgeIn(0, []byte("acct"), []byte("A"), 300))
	mustOK(t, e.Repo(0, []byte("r1"), []byte("acct"), 10000, 365, 10000))
	mustOK(t, e.Repo(0, []byte("r2"), []byte("acct"), 5000, 365, 0))
	mustOK(t, e.CreditCash(400, []byte("acct"), 1))
	// r1 应还 20000，现金 15001 不足，违约：卖 A ceil(20000/99)=203 张
	// 得 20097，余 97 退现金 → 15097；r2 应还 5000，购回 → 10097；再存入 1。
	info1, _ := e.RepoInfo([]byte("r1"))
	info2, _ := e.RepoInfo([]byte("r2"))
	if info1.Status != Defaulted || info2.Status != Redeemed {
		t.Fatalf("r1=%v r2=%v, want Defaulted/Redeemed", info1.Status, info2.Status)
	}
	a := acctOf(t, e, "acct")
	if a.Cash != 10098 {
		t.Fatalf("Cash=%d, want 10098", a.Cash)
	}
	if a.Pos["A"].Pledged != 97 {
		t.Fatalf("Pledged=%d, want 97", a.Pos["A"].Pledged)
	}
}

// 库已空仍未偿清的部分记为坏账；违约释放占用。
func TestBadDebt(t *testing.T) {
	e := New()
	mustOK(t, e.AddBond(0, []byte("A"), 100, 100))
	mustOK(t, e.Credit(0, []byte("acct"), []byte("A"), 100))
	mustOK(t, e.PledgeIn(0, []byte("acct"), []byte("A"), 100))
	mustOK(t, e.Repo(0, []byte("r1"), []byte("acct"), 10000, 365, 10000))
	mustOK(t, e.CreditCash(400, []byte("acct"), 1))
	info, _ := e.RepoInfo([]byte("r1"))
	// 应还 20000，卖 A 100 张仅得 10000，坏账 10000；现金不扣。
	if info.Status != Defaulted || info.BadDebt != 10000 {
		t.Fatalf("info=%+v, want Defaulted BadDebt=10000", info)
	}
	a := acctOf(t, e, "acct")
	if a.Cash != 10001 || a.Cap != 0 || a.Use != 0 {
		t.Fatalf("Cash=%d Cap=%d Use=%d, want 10001/0/0", a.Cash, a.Cap, a.Use)
	}
	if ds := e.Deficits(); len(ds) != 0 {
		t.Fatalf("Deficits=%+v, want 空", ds)
	}
}
