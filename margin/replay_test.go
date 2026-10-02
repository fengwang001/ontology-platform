package margin

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// errCategory 把错误归约为可比较的类别。
func errCategory(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "invalid-param"
	case errors.Is(err, ErrAccountNotFound):
		return "account-not-found"
	case errors.Is(err, ErrPositionConflict):
		return "position-conflict"
	case errors.Is(err, ErrInsufficientFunds):
		return "insufficient-funds"
	default:
		return "unknown"
	}
}

// conservation 跟踪守恒式：
//
//	Z == 全部罚金之和 - 全部 Z 吸收额之和
//	全部亏空之和 == 全部 Z 吸收额 + 全部减仓取走额 + B
type conservation struct {
	fees    int64 // 已扣罚金之和
	used    int64 // Z 吸收额之和
	deficit int64 // 亏空之和（含 Close 产生的）
	taken   int64 // 减仓取走额之和
}

func (c *conservation) check(t *testing.T, e *Engine) {
	t.Helper()
	if z := e.InsuranceFund(); z != c.fees-c.used || z < 0 {
		t.Fatalf("invariant: Z=%d, want fees-used=%d (fees=%d, used=%d)",
			z, c.fees-c.used, c.fees, c.used)
	}
	if b := e.BadDebt(); c.deficit != c.used+c.taken+b || b < 0 {
		t.Fatalf("invariant: deficit=%d, want used+taken+B=%d (used=%d, taken=%d, B=%d)",
			c.deficit, c.used+c.taken+b, c.used, c.taken, b)
	}
}

var replayIDs = []string{"A", "B", "C", "D", "E", "F", "G", "H"}

// checkAllAccounts 对比引擎与朴素模拟的全部账户状态及 Z、B、标记价格，
// 并校验 M、Z、B 非负与被强平账户已清空。
func checkAllAccounts(t *testing.T, e *Engine, n *naive) {
	t.Helper()
	for _, id := range replayIDs {
		m, q, c, ok := e.GetAccount(id)
		na, nok := n.accts[id]
		if ok != nok {
			t.Fatalf("account %s existence: engine=%v naive=%v", id, ok, nok)
		}
		if ok && (m != na.m || q != na.q || c != na.c) {
			t.Fatalf("account %s: engine=(%d,%d,%d) naive=(%d,%d,%d)",
				id, m, q, c, na.m, na.q, na.c)
		}
		if ok && m < 0 {
			t.Fatalf("account %s: negative margin %d", id, m)
		}
	}
	if e.InsuranceFund() != n.z || e.BadDebt() != n.b {
		t.Fatalf("fund mismatch: engine Z=%d B=%d, naive Z=%d B=%d",
			e.InsuranceFund(), e.BadDebt(), n.z, n.b)
	}
	ep, eok := e.MarkPrice()
	if ep != n.markPrice || eok != n.hasMark {
		t.Fatalf("mark price mismatch: engine=(%d,%v) naive=(%d,%v)",
			ep, eok, n.markPrice, n.hasMark)
	}
}

// logMarkBasis 打印 Mark 开始时各账户的权益与维持要求（判定依据）。
func logMarkBasis(t *testing.T, n *naive, p int64) {
	t.Helper()
	for _, id := range replayIDs {
		a, ok := n.accts[id]
		if !ok || a.q == 0 {
			continue
		}
		equity := a.m + a.q*p - a.c
		req := nCeil(nAbs(a.q)*p*n.mm, 10_000)
		t.Logf("  basis %s: M=%d q=%d C=%d E=%d R=%d liquidate=%v",
			id, a.m, a.q, a.c, equity, req, equity < req)
	}
}

// TestRandomReplayAgainstNaive 用 2000 组随机操作序列对照引擎与朴素模拟，
// 每步比较错误类别、强平清单与全部状态，并校验守恒式。
func TestRandomReplayAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		seq := seq
		t.Run(fmt.Sprintf("seq-%d", seq), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
			i := 2 + rng.Int63n(9999) // [2, 10000]
			mm := 1 + rng.Int63n(i-1) // [1, i-1]
			f := rng.Int63n(10001)    // [0, 10000]
			t.Logf("engine params: I=%d Mm=%d F=%d", i, mm, f)

			e, err := NewEngine(i, mm, f)
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}
			n := newNaive(i, mm, f)
			conv := &conservation{}

			ops := 30 + rng.Intn(40)
			for step := 0; step < ops; step++ {
				id := replayIDs[rng.Intn(len(replayIDs))]
				kind := rng.Intn(100)
				switch {
				case kind < 25: // Deposit
					x := 1 + rng.Int63n(20000)
					if rng.Intn(33) == 0 {
						x = []int64{0, -1, 1_000_000_000_001}[rng.Intn(3)]
					}
					errE := e.Deposit(id, x)
					errN := n.deposit(id, x)
					t.Logf("step %d: Deposit(%q, %d) -> %v", step, id, x, errE)
					if errCategory(errE) != errCategory(errN) {
						t.Fatalf("step %d Deposit: engine=%v naive=%v", step, errE, errN)
					}
				case kind < 50: // Open
					dir := Long
					if rng.Intn(2) == 0 {
						dir = Short
					}
					qty := 1 + rng.Int63n(50)
					p := 1 + rng.Int63n(200)
					if rng.Intn(33) == 0 {
						qty = []int64{0, 1_000_001}[rng.Intn(2)]
					}
					errE := e.Open(id, dir, qty, p)
					errN := n.open(id, dir, qty, p)
					t.Logf("step %d: Open(%q, %d, %d, %d) -> %v", step, id, dir, qty, p, errE)
					if errCategory(errE) != errCategory(errN) {
						t.Fatalf("step %d Open: engine=%v naive=%v", step, errE, errN)
					}
				case kind < 65: // Close
					p := 1 + rng.Int63n(300)
					m, q, c, ok := e.GetAccount(id)
					zBefore := e.InsuranceFund()
					errE := e.Close(id, p)
					errN := n.close(id, p)
					t.Logf("step %d: Close(%q, %d) -> %v", step, id, p, errE)
					if errCategory(errE) != errCategory(errN) {
						t.Fatalf("step %d Close: engine=%v naive=%v", step, errE, errN)
					}
					if errE == nil && ok {
						if equity := m + q*p - c; equity < 0 {
							d := -equity
							conv.deficit += d
							conv.used += min(zBefore, d)
						}
					}
				case kind < 75: // Withdraw
					x := 1 + rng.Int63n(20000)
					errE := e.Withdraw(id, x)
					errN := n.withdraw(id, x)
					t.Logf("step %d: Withdraw(%q, %d) -> %v", step, id, x, errE)
					if errCategory(errE) != errCategory(errN) {
						t.Fatalf("step %d Withdraw: engine=%v naive=%v", step, errE, errN)
					}
				default: // Mark
					p := 1 + rng.Int63n(300)
					if rng.Intn(33) == 0 {
						p = []int64{0, 1_000_001}[rng.Intn(2)]
					}
					t.Logf("step %d: Mark(%d)", step, p)
					logMarkBasis(t, n, p)
					recE, errE := e.Mark(p)
					recN, errN := n.mark(p)
					t.Logf("  -> engine records: %+v (err=%v)", recE, errE)
					t.Logf("  -> naive  records: %+v (err=%v)", recN, errN)
					if errCategory(errE) != errCategory(errN) {
						t.Fatalf("step %d Mark: engine=%v naive=%v", step, errE, errN)
					}
					if !recordsEqual(recE, recN) {
						t.Fatalf("step %d Mark records mismatch:\nengine %+v\nnaive  %+v",
							step, recE, recN)
					}
					for _, rec := range recE {
						conv.fees += rec.Fee
						conv.used += rec.FundUsed
						if rec.Equity < 0 {
							conv.deficit += -rec.Equity
						}
						for _, adl := range rec.ADL {
							conv.taken += adl.Take
						}
						// 被强平账户随即无仓位且 C 为 0。
						if _, q, c, _ := e.GetAccount(rec.Account); q != 0 || c != 0 {
							t.Fatalf("step %d: liquidated %s still has q=%d C=%d",
								step, rec.Account, q, c)
						}
					}
				}
				conv.check(t, e)
				checkAllAccounts(t, e, n)
			}
			t.Logf("done: Z=%d B=%d fees=%d used=%d deficit=%d taken=%d",
				e.InsuranceFund(), e.BadDebt(), conv.fees, conv.used, conv.deficit, conv.taken)
		})
	}
}
