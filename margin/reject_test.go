package margin

import (
	"sync"
	"testing"
)

// 构造参数校验：1 <= Mm < I <= 10000 且 0 <= F <= 10000。
func TestNewEngineValidation(t *testing.T) {
	for _, p := range [][3]int64{
		{0, 0, 0},             // Mm < 1
		{100, 100, 0},         // Mm == I
		{500, 1000, 0},        // Mm > I
		{10001, 1, 0},         // I > 10000
		{1000, 500, 10001},    // F > 10000
		{1000, 500, -1},       // F < 0
		{-5, -10, 0},          // 负值
		{10000, 10000, 10000}, // Mm == I 边界
	} {
		if _, err := NewEngine(p[0], p[1], p[2]); err != ErrInvalidParam {
			t.Fatalf("NewEngine%v: got %v, want ErrInvalidParam", p, err)
		}
	}
	if _, err := NewEngine(10000, 9999, 10000); err != nil {
		t.Fatalf("NewEngine(10000, 9999, 10000): %v", err)
	}
	if _, err := NewEngine(2, 1, 0); err != nil {
		t.Fatalf("NewEngine(2, 1, 0): %v", err)
	}
}

// 参数非法优先于账户不存在；账户不存在优先于持仓冲突；
// 持仓冲突优先于资金不足。被拒绝的操作不得改变任何状态。
func TestRejectionPrecedenceAndNoStateChange(t *testing.T) {
	e := mustEngine(t, 1000, 500, 100)
	mustDeposit(t, e, "A", 100)
	mustOpen(t, e, "A", Long, 10, 100)
	mustMark(t, e, 100) // 记录标记价格
	mustDeposit(t, e, "B", 10)

	snapshot := func() [7]int64 {
		m, q, c, _ := e.GetAccount("A")
		mb, qb, cb, _ := e.GetAccount("B")
		p, _ := e.MarkPrice()
		return [7]int64{m, q, c, e.InsuranceFund(), e.BadDebt(), p, mb + qb + cb}
	}
	before := snapshot()

	// 参数非法（优先于账户不存在）。
	wantErr(t, "Deposit empty id", e.Deposit("", 1), ErrInvalidParam)
	wantErr(t, "Deposit x=0", e.Deposit("A", 0), ErrInvalidParam)
	wantErr(t, "Deposit x too large", e.Deposit("A", 1_000_000_000_001), ErrInvalidParam)
	wantErr(t, "Open empty id", e.Open("", Long, 1, 1), ErrInvalidParam)
	wantErr(t, "Open bad side", e.Open("ghost", Side(0), 1, 1), ErrInvalidParam)
	wantErr(t, "Open bad side 3", e.Open("ghost", Side(3), 1, 1), ErrInvalidParam)
	wantErr(t, "Open n=0", e.Open("ghost", Long, 0, 1), ErrInvalidParam)
	wantErr(t, "Open n too large", e.Open("ghost", Long, 1_000_001, 1), ErrInvalidParam)
	wantErr(t, "Open p=0", e.Open("ghost", Long, 1, 0), ErrInvalidParam)
	wantErr(t, "Open p too large", e.Open("ghost", Long, 1, 1_000_001), ErrInvalidParam)
	wantErr(t, "Close p=0", e.Close("ghost", 0), ErrInvalidParam)
	wantErr(t, "Withdraw x=0", e.Withdraw("ghost", 0), ErrInvalidParam)
	if _, err := e.Mark(0); err != ErrInvalidParam {
		t.Fatalf("Mark(0): got %v, want ErrInvalidParam", err)
	}
	if _, err := e.Mark(1_000_001); err != ErrInvalidParam {
		t.Fatalf("Mark(1_000_001): got %v, want ErrInvalidParam", err)
	}

	// 账户不存在（优先于持仓冲突与资金不足）。
	wantErr(t, "Open ghost", e.Open("ghost", Long, 1, 1), ErrAccountNotFound)
	wantErr(t, "Close ghost", e.Close("ghost", 1), ErrAccountNotFound)
	wantErr(t, "Withdraw ghost", e.Withdraw("ghost", 1), ErrAccountNotFound)

	// 持仓冲突或无仓位（优先于资金不足）。
	wantErr(t, "Open opposite", e.Open("A", Short, 1, 1), ErrPositionConflict)
	wantErr(t, "Withdraw with position", e.Withdraw("A", 1_000_000_000_000), ErrPositionConflict)
	wantErr(t, "Close no position", e.Close("B", 1), ErrPositionConflict)

	// 资金不足。
	wantErr(t, "Withdraw too much", e.Withdraw("B", 11), ErrInsufficientFunds)
	wantErr(t, "Open margin short", e.Open("B", Long, 10, 100), ErrInsufficientFunds)

	// 持仓量越界按参数非法处理。
	mustDeposit(t, e, "C", 1_000_000_000)
	mustOpen(t, e, "C", Long, 1_000_000, 1)
	wantErr(t, "Open position overflow", e.Open("C", Long, 1, 1), ErrInvalidParam)

	// 存入后 M 越界按参数非法处理。
	for i := 0; i < 1000; i++ {
		mustDeposit(t, e, "D", 1_000_000_000_000)
	}
	wantErr(t, "Deposit margin overflow", e.Deposit("D", 1), ErrInvalidParam)

	if after := snapshot(); before != after {
		t.Fatalf("rejected ops changed state: before %v, after %v", before, after)
	}
	checkAccount(t, e, "B", 10, 0, 0)
	checkAccount(t, e, "C", 1_000_000_000, 1_000_000, 1_000_000)
	checkAccount(t, e, "D", 1_000_000_000_000_000, 0, 0)
}

// 相同操作序列重放得到完全相同的结果。
func TestReplayDeterministic(t *testing.T) {
	run := func() ([]Liquidation, [4]int64) {
		e := mustEngine(t, 1000, 500, 100)
		mustDeposit(t, e, "L1", 85)
		mustOpen(t, e, "L1", Long, 10, 85)
		mustDeposit(t, e, "L2", 100)
		mustOpen(t, e, "L2", Long, 10, 100)
		mustDeposit(t, e, "S1", 100)
		mustOpen(t, e, "S1", Short, 10, 100)
		mustDeposit(t, e, "S2", 20)
		mustOpen(t, e, "S2", Short, 1, 200)
		recs := mustMark(t, e, 80)
		m, q, c, _ := e.GetAccount("S2")
		return recs, [4]int64{m, q, c, e.InsuranceFund()}
	}
	recs1, state1 := run()
	recs2, state2 := run()
	if !recordsEqual(recs1, recs2) || state1 != state2 {
		t.Fatalf("replay mismatch:\n%v %v\n%v %v", recs1, state1, recs2, state2)
	}
}

func recordsEqual(a, b []Liquidation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Account != y.Account || x.Equity != y.Equity || x.Fee != y.Fee ||
			x.FundUsed != y.FundUsed || x.BadDebt != y.BadDebt ||
			len(x.ADL) != len(y.ADL) {
			return false
		}
		for j := range x.ADL {
			if x.ADL[j] != y.ADL[j] {
				return false
			}
		}
	}
	return true
}

// 并发调用：结果等价于某个串行顺序，且不变量始终成立。
func TestConcurrentAccess(t *testing.T) {
	e := mustEngine(t, 1000, 500, 100)
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('A' + i%8))
			_ = e.Deposit(id, 100)
			_ = e.Open(id, Long, 1, 100)
			_, _ = e.Mark(90 + int64(i%20))
			_, _, _, _ = e.GetAccount(id)
			_ = e.InsuranceFund()
			_ = e.BadDebt()
			_ = e.Close(id, 95)
			_ = e.Withdraw(id, 1)
		}(i)
	}
	wg.Wait()
	if e.InsuranceFund() < 0 {
		t.Fatalf("insurance fund negative: %d", e.InsuranceFund())
	}
	if e.BadDebt() < 0 {
		t.Fatalf("bad debt negative: %d", e.BadDebt())
	}
}
