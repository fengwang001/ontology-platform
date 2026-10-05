package creation

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/basket"
)

func mustProc(t *testing.T, items []basket.Item, e, rmax, q int64) *Processor {
	t.Helper()
	p, err := New(items, e, rmax, q)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// specItems 为题面示例清单：a(100股,N)、b(200股,A,prem=1050)、c(M,fixed=5000)，E=-300，Rmax=60。
func specItems() []basket.Item {
	return []basket.Item{
		{Sym: "a", Qty: 100, Flag: basket.FlagN},
		{Sym: "b", Qty: 200, Flag: basket.FlagA, Prem: 1050},
		{Sym: "c", Qty: 1, Flag: basket.FlagM, Fixed: 5000},
	}
}

// newSpecProc 建好示例处理器：价格 a=10、b=21，账户 u 持 a 100 股、b 150 股、现金 10000。
func newSpecProc(t *testing.T) *Processor {
	t.Helper()
	p := mustProc(t, specItems(), -300, 60, 10)
	mustOK(t, p.SetPrice(1, "a", 10))
	mustOK(t, p.SetPrice(1, "b", 21))
	mustOK(t, p.Credit(2, "u", "a", 100))
	mustOK(t, p.Credit(2, "u", "b", 150))
	mustOK(t, p.CreditCash(2, "u", 10000))
	return p
}

// TestSpecExampleCreate 复现题面申购示例：预收 1161、净额 5861、现金余 4139。
func TestSpecExampleCreate(t *testing.T) {
	p := newSpecProc(t)
	mustOK(t, p.Create(3, "id1", "u", 1))
	if got := p.Cash("u"); got != 4139 {
		t.Fatalf("cash = %d, want 4139", got)
	}
	if got := p.Holding("u", "a"); got != 0 {
		t.Fatalf("hold a = %d, want 0", got)
	}
	if got := p.Holding("u", "b"); got != 0 {
		t.Fatalf("hold b = %d, want 0", got)
	}
	if got := p.FundInv("a"); got != 100 {
		t.Fatalf("fund a = %d, want 100", got)
	}
	if got := p.FundInv("b"); got != 150 {
		t.Fatalf("fund b = %d, want 150", got)
	}
	if got := p.FundCash(); got != 5861 {
		t.Fatalf("fund cash = %d, want 5861", got)
	}
	if got := p.Shares("u"); got != 1 {
		t.Fatalf("shares = %d, want 1", got)
	}
	// 当日申购所得份额当日不可赎回。
	if err := p.Redeem(4, "u", 1); !errors.Is(err, ErrShares) {
		t.Fatalf("same-day redeem err = %v, want ErrShares", err)
	}
}

// TestSpecExampleRatioReject 只持 b 140 股时 X=6260，626000>612000 报替代比例超限。
func TestSpecExampleRatioReject(t *testing.T) {
	p := mustProc(t, specItems(), -300, 60, 10)
	mustOK(t, p.SetPrice(1, "a", 10))
	mustOK(t, p.SetPrice(1, "b", 21))
	mustOK(t, p.Credit(2, "u", "a", 100))
	mustOK(t, p.Credit(2, "u", "b", 140))
	mustOK(t, p.CreditCash(2, "u", 10000))
	if err := p.Create(3, "id1", "u", 1); !errors.Is(err, ErrRatio) {
		t.Fatalf("err = %v, want ErrRatio", err)
	}
}

// TestSpecExampleEndOfDayRefund 日终 b 现价 22：成本 1100，退 61；次日赎回净收 5684。
func TestSpecExampleEndOfDayRefund(t *testing.T) {
	p := newSpecProc(t)
	mustOK(t, p.Create(3, "id1", "u", 1))
	mustOK(t, p.SetPrice(4, "b", 22))
	mustOK(t, p.EndOfDay(5))
	if got := p.Cash("u"); got != 4200 {
		t.Fatalf("cash = %d, want 4200（退 61）", got)
	}
	if got := p.FundCash(); got != 5800 {
		t.Fatalf("fund cash = %d, want 5800", got)
	}
	// 次日赎回：b 缺口 50 按现价 22 付 floor(984.5)=984，c 付 5000，差额向账户收 300。
	mustOK(t, p.Redeem(6, "u", 1))
	if got := p.Cash("u"); got != 9884 {
		t.Fatalf("cash = %d, want 9884（净收 5684）", got)
	}
	if got := p.Holding("u", "a"); got != 100 {
		t.Fatalf("hold a = %d, want 100", got)
	}
	if got := p.Holding("u", "b"); got != 150 {
		t.Fatalf("hold b = %d, want 150", got)
	}
	if got := p.Shares("u"); got != 0 {
		t.Fatalf("shares = %d, want 0", got)
	}
}

// TestSpecExampleEndOfDayCollect 日终 b 现价 24：成本 1200，补收 39。
func TestSpecExampleEndOfDayCollect(t *testing.T) {
	p := newSpecProc(t)
	mustOK(t, p.Create(3, "id1", "u", 1))
	mustOK(t, p.SetPrice(4, "b", 24))
	mustOK(t, p.EndOfDay(5))
	if got := p.Cash("u"); got != 4100 {
		t.Fatalf("cash = %d, want 4100（补收 39）", got)
	}
	if got := p.Debt("u"); got != 0 {
		t.Fatalf("debt = %d, want 0", got)
	}
}

// TestEndOfDayCollectDebt 补收不足时扣到 0，差额记入欠款。
func TestEndOfDayCollectDebt(t *testing.T) {
	p := mustProc(t, specItems(), -300, 60, 10)
	mustOK(t, p.SetPrice(1, "a", 10))
	mustOK(t, p.SetPrice(1, "b", 21))
	mustOK(t, p.Credit(2, "u", "a", 100))
	mustOK(t, p.Credit(2, "u", "b", 150))
	mustOK(t, p.CreditCash(2, "u", 5870)) // 净额 5861，申购后余 9
	mustOK(t, p.Create(3, "id1", "u", 1))
	mustOK(t, p.SetPrice(4, "b", 24)) // 补收 39，只能扣 9
	mustOK(t, p.EndOfDay(5))
	if got := p.Cash("u"); got != 0 {
		t.Fatalf("cash = %d, want 0", got)
	}
	if got := p.Debt("u"); got != 30 {
		t.Fatalf("debt = %d, want 30", got)
	}
	if got := p.FundCash(); got != 5870 {
		t.Fatalf("fund cash = %d, want 5870", got)
	}
}

// TestRoundingDirection 申购预收向上取整、赎回偿付向下取整。
func TestRoundingDirection(t *testing.T) {
	items := []basket.Item{{Sym: "s", Qty: 1, Flag: basket.FlagA, Prem: 1050}}
	p := mustProc(t, items, 0, 100, 10)
	mustOK(t, p.SetPrice(1, "s", 3))
	mustOK(t, p.CreditCash(1, "u", 100))
	// 缺口 1：预收 ceil(3*11050/10000)=ceil(3.315)=4。
	mustOK(t, p.Create(2, "id1", "u", 1))
	if got := p.Cash("u"); got != 96 {
		t.Fatalf("cash = %d, want 96", got)
	}
	// 日终现价仍为 3：成本 3，退 1。
	mustOK(t, p.EndOfDay(3))
	if got := p.Cash("u"); got != 97 {
		t.Fatalf("cash = %d, want 97", got)
	}
	// 次日赎回：基金无库存，缺口 1 付 floor(3*8950/10000)=floor(2.685)=2。
	mustOK(t, p.Redeem(4, "u", 1))
	if got := p.Cash("u"); got != 99 {
		t.Fatalf("cash = %d, want 99", got)
	}
}

// TestRatioBoundary 替代比例取等通过、超出 1 拒绝。
func TestRatioBoundary(t *testing.T) {
	// M fixed=1（X=1），N 价值 qty*1 决定 Y=1+qty；Rmax=1。
	mk := func(qty int64) *Processor {
		items := []basket.Item{
			{Sym: "n", Qty: qty, Flag: basket.FlagN},
			{Sym: "m", Qty: 1, Flag: basket.FlagM, Fixed: 1},
		}
		p := mustProc(t, items, 0, 1, 10)
		mustOK(t, p.SetPrice(1, "n", 1))
		mustOK(t, p.Credit(1, "u", "n", qty))
		mustOK(t, p.CreditCash(1, "u", 100))
		return p
	}
	// Y=100：X*100=100 <= 1*100=100，取等通过。
	mustOK(t, mk(99).Create(2, "id1", "u", 1))
	// Y=99：X*100=100 > 1*99=99，多 1 拒绝。
	if err := mk(98).Create(2, "id1", "u", 1); !errors.Is(err, ErrRatio) {
		t.Fatalf("err = %v, want ErrRatio", err)
	}
}

// TestNegativeENetNonPositiveExempt E 为负使净额不为正时免检资金。
func TestNegativeENetNonPositiveExempt(t *testing.T) {
	items := []basket.Item{{Sym: "n", Qty: 1, Flag: basket.FlagN}}
	p := mustProc(t, items, -1_000_000_000, 0, 10)
	mustOK(t, p.SetPrice(1, "n", 1))
	mustOK(t, p.Credit(1, "u", "n", 1)) // 现金 0
	mustOK(t, p.Create(2, "id1", "u", 1))
	if got := p.Cash("u"); got != 1_000_000_000 {
		t.Fatalf("cash = %d, want 1e9（收到 -E*n）", got)
	}
}

// TestQuotaBoundary 当日额度取等通过、超 1 拒绝，日终清零。
func TestQuotaBoundary(t *testing.T) {
	items := []basket.Item{{Sym: "n", Qty: 1, Flag: basket.FlagN}}
	p := mustProc(t, items, 0, 0, 2)
	mustOK(t, p.SetPrice(1, "n", 1))
	mustOK(t, p.Credit(1, "u", "n", 10))
	mustOK(t, p.Create(2, "id1", "u", 2)) // 取等通过
	if err := p.Create(2, "id2", "u", 1); !errors.Is(err, ErrQuota) {
		t.Fatalf("err = %v, want ErrQuota", err)
	}
	mustOK(t, p.EndOfDay(3))
	// 日终清零额度；被拒绝的 id2 未占用，可复用。
	mustOK(t, p.Create(4, "id2", "u", 1))
}

// TestShareLockUnlock 当日申购份额锁定、日终解锁。
func TestShareLockUnlock(t *testing.T) {
	items := []basket.Item{{Sym: "n", Qty: 1, Flag: basket.FlagN}}
	p := mustProc(t, items, 0, 0, 10)
	mustOK(t, p.SetPrice(1, "n", 1))
	mustOK(t, p.Credit(1, "u", "n", 5))
	mustOK(t, p.Create(2, "id1", "u", 2))
	if err := p.Redeem(2, "u", 1); !errors.Is(err, ErrShares) {
		t.Fatalf("err = %v, want ErrShares", err)
	}
	mustOK(t, p.EndOfDay(3))
	mustOK(t, p.Redeem(4, "u", 2))
	if got := p.Shares("u"); got != 0 {
		t.Fatalf("shares = %d, want 0", got)
	}
}

// TestComponentShortageByteOrder N 类不足报字节序最小的一项。
func TestComponentShortageByteOrder(t *testing.T) {
	items := []basket.Item{
		{Sym: "z", Qty: 10, Flag: basket.FlagN},
		{Sym: "a", Qty: 10, Flag: basket.FlagN},
		{Sym: "m", Qty: 5, Flag: basket.FlagN},
	}
	p := mustProc(t, items, 0, 0, 10)
	for _, s := range []string{"a", "m", "z"} {
		mustOK(t, p.SetPrice(1, s, 1))
	}
	mustOK(t, p.Credit(1, "u", "z", 10)) // a、m 不足
	err := p.Create(2, "id1", "u", 1)
	var se *SymbolError
	if !errors.As(err, &se) || !errors.Is(err, ErrComponent) || se.Sym != "a" {
		t.Fatalf("err = %v, want ErrComponent sym=a", err)
	}
}

// TestInventoryShortageByteOrder 赎回时基金库存不足报字节序最小项。
// （正常流程下基金 N 类库存恒足，此处直接构造内部状态验证防御性校验。）
func TestInventoryShortageByteOrder(t *testing.T) {
	items := []basket.Item{
		{Sym: "b", Qty: 10, Flag: basket.FlagN},
		{Sym: "a", Qty: 10, Flag: basket.FlagN},
	}
	p := mustProc(t, items, 0, 0, 10)
	mustOK(t, p.SetPrice(1, "a", 1))
	mustOK(t, p.SetPrice(1, "b", 1))
	mustOK(t, p.CreditCash(1, "u", 1))
	p.accts["u"].shares = 1
	p.fundInv["b"] = 10
	err := p.Redeem(2, "u", 1)
	var se *SymbolError
	if !errors.As(err, &se) || !errors.Is(err, ErrInventory) || se.Sym != "a" {
		t.Fatalf("err = %v, want ErrInventory sym=a", err)
	}
}

// TestRejectOrder 拒绝按固定次序只报第一个。
func TestRejectOrder(t *testing.T) {
	items := []basket.Item{
		{Sym: "n", Qty: 10, Flag: basket.FlagN},
		{Sym: "s", Qty: 10, Flag: basket.FlagA, Prem: 100},
	}
	newP := func(t *testing.T, rmax int64) *Processor {
		p := mustProc(t, items, 0, rmax, 1)
		mustOK(t, p.SetPrice(1, "n", 1))
		mustOK(t, p.SetPrice(1, "s", 1))
		mustOK(t, p.Credit(1, "u", "n", 100))
		mustOK(t, p.CreditCash(1, "u", 100))
		return p
	}
	t.Run("参数非法优先于时钟回退", func(t *testing.T) {
		p := newP(t, 100)
		if err := p.Create(0, "id1", "u", 0); !errors.Is(err, basket.ErrParam) {
			t.Fatalf("err = %v, want ErrParam", err)
		}
	})
	t.Run("时钟回退优先于不存在", func(t *testing.T) {
		p := newP(t, 100)
		if err := p.Create(0, "id1", "ghost", 1); !errors.Is(err, ErrClock) {
			t.Fatalf("err = %v, want ErrClock", err)
		}
	})
	t.Run("不存在优先于当日额度", func(t *testing.T) {
		p := newP(t, 100)
		mustOK(t, p.Create(2, "id1", "u", 1)) // 额度用满
		if err := p.Create(3, "id2", "ghost", 1); !errors.Is(err, ErrNotExist) {
			t.Fatalf("err = %v, want ErrNotExist（账户未建立）", err)
		}
		if err := p.Create(3, "id1", "u", 1); !errors.Is(err, ErrNotExist) {
			t.Fatalf("err = %v, want ErrNotExist（id 重复）", err)
		}
	})
	t.Run("无现价报不存在", func(t *testing.T) {
		p := mustProc(t, items, 0, 100, 1)
		mustOK(t, p.CreditCash(1, "u", 100))
		if err := p.Create(2, "id1", "u", 1); !errors.Is(err, ErrNotExist) {
			t.Fatalf("err = %v, want ErrNotExist（无现价）", err)
		}
	})
	t.Run("当日额度优先于成分券不足", func(t *testing.T) {
		p := newP(t, 100)
		mustOK(t, p.Create(2, "id1", "u", 1)) // 额度用满
		mustOK(t, p.CreditCash(2, "v", 100))  // v 无成分券
		if err := p.Create(3, "id2", "v", 1); !errors.Is(err, ErrQuota) {
			t.Fatalf("err = %v, want ErrQuota", err)
		}
	})
	t.Run("成分券不足优先于替代比例超限", func(t *testing.T) {
		p := newP(t, 0) // Rmax=0，A 类缺口必超限
		mustOK(t, p.CreditCash(2, "v", 100))
		if err := p.Create(3, "id1", "v", 1); !errors.Is(err, ErrComponent) {
			t.Fatalf("err = %v, want ErrComponent", err)
		}
	})
	t.Run("替代比例超限优先于资金不足", func(t *testing.T) {
		p := newP(t, 0)                      // Rmax=0
		mustOK(t, p.Credit(2, "v", "n", 10)) // 成分券够、无现金
		if err := p.Create(3, "id1", "v", 1); !errors.Is(err, ErrRatio) {
			t.Fatalf("err = %v, want ErrRatio", err)
		}
	})
	t.Run("份额不足优先于库存不足", func(t *testing.T) {
		p := newP(t, 100)
		mustOK(t, p.CreditCash(2, "v", 100))
		if err := p.Redeem(3, "v", 1); !errors.Is(err, ErrShares) {
			t.Fatalf("err = %v, want ErrShares", err)
		}
	})
	t.Run("库存不足优先于资金不足", func(t *testing.T) {
		negE := mustProc(t, []basket.Item{{Sym: "n", Qty: 1, Flag: basket.FlagN}}, -100, 0, 10)
		mustOK(t, negE.SetPrice(1, "n", 1))
		mustOK(t, negE.CreditCash(1, "u", 1))
		negE.accts["u"].shares = 1 // 基金无库存且赎回净收入为负、现金不足
		if err := negE.Redeem(2, "u", 1); !errors.Is(err, ErrInventory) {
			t.Fatalf("err = %v, want ErrInventory", err)
		}
	})
	t.Run("赎回资金不足", func(t *testing.T) {
		negE := mustProc(t, []basket.Item{{Sym: "n", Qty: 1, Flag: basket.FlagN}}, -100, 0, 10)
		mustOK(t, negE.SetPrice(1, "n", 1))
		mustOK(t, negE.CreditCash(1, "u", 50))
		negE.accts["u"].shares = 1
		negE.fundInv["n"] = 1
		if err := negE.Redeem(2, "u", 1); !errors.Is(err, ErrFund) {
			t.Fatalf("err = %v, want ErrFund", err)
		}
	})
}

// TestAllOrNothing 被拒绝的 Create/Redeem 不改任何状态（含时钟）。
func TestAllOrNothing(t *testing.T) {
	p := newSpecProc(t)
	snap := func() string {
		return fmt.Sprintf("cash=%d a=%d b=%d shares=%d fa=%d fb=%d fc=%d day=%d ids=%d pend=%d now=%d",
			p.Cash("u"), p.Holding("u", "a"), p.Holding("u", "b"), p.Shares("u"),
			p.FundInv("a"), p.FundInv("b"), p.FundCash(), p.dayUnits, len(p.ids), len(p.pending), p.maxNow)
	}
	before := snap()
	if err := p.Create(100, "idX", "u", 2); !errors.Is(err, ErrComponent) {
		t.Fatalf("err = %v, want ErrComponent", err)
	}
	if err := p.Redeem(100, "u", 1); !errors.Is(err, ErrShares) {
		t.Fatalf("err = %v, want ErrShares", err)
	}
	if after := snap(); after != before {
		t.Fatalf("被拒绝操作改变了状态:\nbefore %s\nafter  %s", before, after)
	}
	// 时钟未被拒绝操作推进：now=50 仍被接受。
	mustOK(t, p.CreditCash(50, "u", 1))
	// 被拒绝的 id 未占用。
	mustOK(t, p.Create(51, "idX", "u", 1))
}

// TestTouched 一次 Create 触碰的账户持券记录数不超过清单项数，
// 与账户持有的清单外证券种数无关（10 与 10000 两档对照）。
func TestTouched(t *testing.T) {
	items := []basket.Item{
		{Sym: "a", Qty: 1, Flag: basket.FlagN},
		{Sym: "b", Qty: 1, Flag: basket.FlagA, Prem: 100},
		{Sym: "c", Qty: 1, Flag: basket.FlagM, Fixed: 1},
	}
	run := func(extra int) int {
		p := mustProc(t, items, 0, 100, 10)
		mustOK(t, p.SetPrice(1, "a", 1))
		mustOK(t, p.SetPrice(1, "b", 1))
		mustOK(t, p.Credit(1, "u", "a", 1))
		mustOK(t, p.Credit(1, "u", "b", 1))
		mustOK(t, p.CreditCash(1, "u", 100))
		for i := 0; i < extra; i++ {
			mustOK(t, p.Credit(1, "u", fmt.Sprintf("x%05d", i), 1))
		}
		mustOK(t, p.Create(2, "id1", "u", 1))
		return p.touched
	}
	t10 := run(10)
	t10000 := run(10000)
	if t10 != t10000 {
		t.Fatalf("touched 随清单外证券种数变化：%d vs %d", t10, t10000)
	}
	if t10 > len(items) {
		t.Fatalf("touched = %d, want <= 清单项数 %d", t10, len(items))
	}
}

// TestConcurrent 并发调用等价于某个串行顺序：额度与守恒不变式成立。
func TestConcurrent(t *testing.T) {
	items := []basket.Item{{Sym: "n", Qty: 1, Flag: basket.FlagN}}
	p := mustProc(t, items, 0, 0, 100)
	mustOK(t, p.SetPrice(1, "n", 1))
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			acct := fmt.Sprintf("u%d", i)
			_ = p.Credit(2, acct, "n", 1)
			_ = p.CreditCash(2, acct, 1)
		}(i)
	}
	wg.Wait()
	// 200 个 goroutine 各申购 1 单位，额度 100：恰好 100 笔成功。
	var okCount atomic.Int64
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			acct := fmt.Sprintf("u%d", i%100)
			err := p.Create(3, fmt.Sprintf("id%d", i), acct, 1)
			switch {
			case err == nil:
				okCount.Add(1)
			case errors.Is(err, ErrQuota), errors.Is(err, ErrComponent):
			default:
				t.Errorf("unexpected err: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if okCount.Load() != 100 {
		t.Fatalf("accepted = %d, want 100", okCount.Load())
	}
	var totalShares, totalHolds int64
	for i := 0; i < 100; i++ {
		acct := fmt.Sprintf("u%d", i)
		totalShares += p.Shares(acct)
		totalHolds += p.Holding(acct, "n")
	}
	if totalShares != 100 {
		t.Fatalf("total shares = %d, want 100", totalShares)
	}
	if got := totalHolds + p.FundInv("n"); got != 100 {
		t.Fatalf("holds+fundInv = %d, want 100", got)
	}
}
