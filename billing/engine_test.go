package billing

import (
	"errors"
	"sync"
	"testing"
)

// naiveEngine 是按规格公式逐行重算的朴素参考实现，用于与 Engine 对照。
type naiveEngine struct {
	days          int
	prices        map[string]int64
	plan          string
	lastChangeDay int
	hasChange     bool
	lines         []Line
}

func newNaive(days int, prices map[string]int64, initial string) *naiveEngine {
	n := &naiveEngine{days: days, prices: prices}
	n.startCycle(initial)
	return n
}

func (n *naiveEngine) startCycle(plan string) {
	n.plan = plan
	n.hasChange = false
	n.lines = []Line{{Seq: 0, Kind: Prepay, Plan: plan, Day: 0, Amount: n.prices[plan]}}
}

// change 按规格公式重算一次变更：折退向下取整、补收向上取整。
func (n *naiveEngine) change(day int, newPlan string) error {
	newPrice, ok := n.prices[newPlan]
	if !ok {
		return ErrUnknownPlan
	}
	if newPlan == n.plan {
		return ErrSamePlan
	}
	if day < 1 || day > n.days-1 {
		return ErrInvalidDay
	}
	if n.hasChange {
		if day == n.lastChangeDay {
			return ErrSameDayChange
		}
		if day < n.lastChangeDay {
			return ErrOutOfOrderDay
		}
	}
	r := int64(n.days - day)
	d := int64(n.days)
	refund := Line{Seq: len(n.lines), Kind: Refund, Plan: n.plan, Day: day,
		Amount: -(n.prices[n.plan] * r / d)}
	surcharge := Line{Seq: len(n.lines) + 1, Kind: Surcharge, Plan: newPlan, Day: day,
		Amount: (newPrice*r + d - 1) / d}
	n.lines = append(n.lines, refund, surcharge)
	n.plan = newPlan
	n.lastChangeDay = day
	n.hasChange = true
	return nil
}

func (n *naiveEngine) settle() ([]Line, int64) {
	lines := n.lines
	var total int64
	for _, l := range lines {
		total += l.Amount
	}
	n.startCycle(n.plan)
	return lines, total
}

func sumLines(lines []Line) int64 {
	var total int64
	for _, l := range lines {
		total += l.Amount
	}
	return total
}

func logLines(t *testing.T, label string, lines []Line) {
	t.Helper()
	for _, l := range lines {
		t.Logf("%s %s", label, l)
	}
}

var testPrices = map[string]int64{
	"basic": 1000,
	"pro":   3000,
	"elite": 9999,
	"free":  0,
}

func mustEngine(t *testing.T, days int, initial string) *Engine {
	t.Helper()
	e, err := NewEngine(days, testPrices, initial)
	if err != nil {
		t.Fatalf("NewEngine(%d, %q) 失败: %v", days, initial, err)
	}
	return e
}

// TestFloorCeilOneCent 覆盖整除与不整除时向下/向上取整，不整除时两者差一分。
func TestFloorCeilOneCent(t *testing.T) {
	// 整除：D=10, basic=1000, day=5, r=5 -> 1000*5/10=500, 3000*5/10=1500。
	e := mustEngine(t, 10, "basic")
	refund, surcharge, err := e.ChangePlan(5, "pro")
	if err != nil {
		t.Fatalf("ChangePlan 失败: %v", err)
	}
	t.Logf("输入: D=10 basic=1000 pro=3000 day=5 r=5")
	t.Logf("输出: refund=%d surcharge=%d", refund.Amount, surcharge.Amount)
	t.Logf("判定: 1000*5/10=500 整除 floor=ceil; 3000*5/10=1500 整除")
	if refund.Amount != -500 || surcharge.Amount != 1500 {
		t.Fatalf("整除情形错误: refund=%d surcharge=%d, 期望 -500/1500", refund.Amount, surcharge.Amount)
	}

	// 不整除：D=3, 两套餐同价 1000, day=1, r=2 -> 折退 floor(2000/3)=666, 补收 ceil(2000/3)=667, 差一分。
	prices := map[string]int64{"a": 1000, "b": 1000}
	e2, err := NewEngine(3, prices, "a")
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	refund2, surcharge2, err := e2.ChangePlan(1, "b")
	if err != nil {
		t.Fatalf("ChangePlan 失败: %v", err)
	}
	t.Logf("输入: D=3 a=1000 b=1000 day=1 r=2")
	t.Logf("输出: refund=%d surcharge=%d", refund2.Amount, surcharge2.Amount)
	t.Logf("判定: floor(2000/3)=666, ceil(2000/3)=667, 折退不多退、补收不少收, 差一分")
	if refund2.Amount != -666 || surcharge2.Amount != 667 {
		t.Fatalf("不整除情形错误: refund=%d surcharge=%d, 期望 -666/667", refund2.Amount, surcharge2.Amount)
	}
	if surcharge2.Amount+refund2.Amount != 1 {
		t.Fatalf("不整除时折退与补收应差一分: 净差 %d", surcharge2.Amount+refund2.Amount)
	}
}

// TestBoundaryDays 覆盖 day=1 与 day=D-1 两个边界变更日。
func TestBoundaryDays(t *testing.T) {
	e := mustEngine(t, 30, "basic")

	// day=1: r=29, 折退 floor(1000*29/30)=966, 补收 ceil(3000*29/30)=2900。
	refund, surcharge, err := e.ChangePlan(1, "pro")
	if err != nil {
		t.Fatalf("day=1 变更失败: %v", err)
	}
	t.Logf("输入: D=30 day=1 r=29 basic=1000 pro=3000")
	t.Logf("输出: refund=%d surcharge=%d", refund.Amount, surcharge.Amount)
	t.Logf("判定: floor(29000/30)=966, ceil(87000/30)=2900")
	if refund.Amount != -966 || surcharge.Amount != 2900 {
		t.Fatalf("day=1 错误: refund=%d surcharge=%d, 期望 -966/2900", refund.Amount, surcharge.Amount)
	}

	// day=D-1=29: r=1, 折退 floor(3000/30)=100, 补收 ceil(9999/30)=334。
	refund2, surcharge2, err := e.ChangePlan(29, "elite")
	if err != nil {
		t.Fatalf("day=D-1 变更失败: %v", err)
	}
	t.Logf("输入: D=30 day=29 r=1 pro=3000 elite=9999")
	t.Logf("输出: refund=%d surcharge=%d", refund2.Amount, surcharge2.Amount)
	t.Logf("判定: floor(3000/30)=100, ceil(9999/30)=334")
	if refund2.Amount != -100 || surcharge2.Amount != 334 {
		t.Fatalf("day=D-1 错误: refund=%d surcharge=%d, 期望 -100/334", refund2.Amount, surcharge2.Amount)
	}
}

// TestThreeChangesSecondRefundUsesSecondPlan 同一周期三次变更，
// 第二次折退必须按第二个套餐（pro）的标价计，而非已支付金额。
func TestThreeChangesSecondRefundUsesSecondPlan(t *testing.T) {
	e := mustEngine(t, 30, "basic")
	steps := []struct {
		day  int
		plan string
	}{
		{5, "pro"},
		{10, "elite"},
		{20, "free"},
	}
	for _, s := range steps {
		if _, _, err := e.ChangePlan(s.day, s.plan); err != nil {
			t.Fatalf("ChangePlan(%d, %q) 失败: %v", s.day, s.plan, err)
		}
	}
	lines := e.Lines()
	logLines(t, "三次变更后:", lines)
	t.Logf("判定: 第2次折退按 pro 标价 floor(3000*20/30)=2000; 第3次折退按 elite 标价 floor(9999*10/30)=3333")

	if len(lines) != 7 {
		t.Fatalf("行数错误: got %d, 期望 7 (1 预收 + 3x2)", len(lines))
	}
	// 行序: 0 预收, 1/2 第一次变更, 3/4 第二次变更, 5/6 第三次变更。
	second := lines[3]
	if second.Kind != Refund || second.Plan != "pro" || second.Amount != -2000 {
		t.Fatalf("第二次折退错误: %+v, 期望按 pro 标价 -2000", second)
	}
	third := lines[5]
	if third.Kind != Refund || third.Plan != "elite" || third.Amount != -3333 {
		t.Fatalf("第三次折退错误: %+v, 期望按 elite 标价 -3333", third)
	}
	// 各行之和恒等于应收总额。
	if got, want := sumLines(lines), e.Total(); got != want {
		t.Fatalf("行之和 %d != Total %d", got, want)
	}
	t.Logf("应收总额 Total=%d", e.Total())
}

// TestUpgradeThenImmediateDowngrade 升级后立刻再降级，校验净额。
func TestUpgradeThenImmediateDowngrade(t *testing.T) {
	e := mustEngine(t, 10, "basic")
	if _, _, err := e.ChangePlan(5, "pro"); err != nil {
		t.Fatalf("升级失败: %v", err)
	}
	if _, _, err := e.ChangePlan(6, "basic"); err != nil {
		t.Fatalf("降级失败: %v", err)
	}
	lines := e.Lines()
	logLines(t, "升级后立即降级:", lines)
	// 预收 1000; 折退 -500, 补收 1500; 折退 -floor(3000*4/10)=-1200, 补收 ceil(1000*4/10)=400。
	want := []int64{1000, -500, 1500, -1200, 400}
	var net int64
	for i, w := range want {
		if lines[i].Amount != w {
			t.Fatalf("行 %d 金额错误: got %d, 期望 %d", i, lines[i].Amount, w)
		}
		net += w
	}
	t.Logf("判定: 净额 = 1000-500+1500-1200+400 = %d", net)
	if e.Total() != net {
		t.Fatalf("净额错误: got %d, 期望 %d", e.Total(), net)
	}
}

// TestZeroPricePlan 价格为 0 的套餐：补收为 0，且从 0 价套餐折退也为 0。
func TestZeroPricePlan(t *testing.T) {
	e := mustEngine(t, 30, "basic")
	_, surcharge, err := e.ChangePlan(10, "free")
	if err != nil {
		t.Fatalf("变更到 free 失败: %v", err)
	}
	t.Logf("输入: D=30 day=10 basic=1000 -> free=0")
	t.Logf("输出: surcharge=%d", surcharge.Amount)
	t.Logf("判定: ceil(0*20/30)=0")
	if surcharge.Amount != 0 {
		t.Fatalf("0 价套餐补收应为 0, got %d", surcharge.Amount)
	}
	refund, surcharge2, err := e.ChangePlan(20, "pro")
	if err != nil {
		t.Fatalf("从 free 变更失败: %v", err)
	}
	t.Logf("输入: D=30 day=20 free=0 -> pro=3000")
	t.Logf("输出: refund=%d surcharge=%d", refund.Amount, surcharge2.Amount)
	t.Logf("判定: 折退按 free 标价 floor(0*10/30)=0; 补收 ceil(3000*10/30)=1000")
	if refund.Amount != 0 || surcharge2.Amount != 1000 {
		t.Fatalf("0 价套餐折退/补收错误: refund=%d surcharge=%d", refund.Amount, surcharge2.Amount)
	}
}

// TestSettleStartsNewCycle 结算返回全部行与代数和，并开启新周期重新预收。
func TestSettleStartsNewCycle(t *testing.T) {
	e := mustEngine(t, 30, "basic")
	if _, _, err := e.ChangePlan(10, "pro"); err != nil {
		t.Fatalf("变更失败: %v", err)
	}
	lines, total := e.Settle()
	logLines(t, "第一周期结算:", lines)
	t.Logf("输出: total=%d", total)
	t.Logf("判定: total 等于各行代数和; 新周期按当前套餐 pro 在第 0 天预收全价 3000")
	if got := sumLines(lines); got != total {
		t.Fatalf("结算总额 %d != 行之和 %d", total, got)
	}
	if len(lines) != 3 {
		t.Fatalf("第一周期行数错误: got %d, 期望 3", len(lines))
	}

	newLines := e.Lines()
	logLines(t, "第二周期初始:", newLines)
	if len(newLines) != 1 || newLines[0].Kind != Prepay || newLines[0].Plan != "pro" ||
		newLines[0].Day != 0 || newLines[0].Amount != 3000 || newLines[0].Seq != 0 {
		t.Fatalf("新周期预收行错误: %+v", newLines)
	}
	if e.Cycle() != 2 || e.CurrentPlan() != "pro" {
		t.Fatalf("新周期状态错误: cycle=%d plan=%s", e.Cycle(), e.CurrentPlan())
	}
	// 上一周期的变更日限制已重置：可再次在 day=10 变更。
	if _, _, err := e.ChangePlan(10, "elite"); err != nil {
		t.Fatalf("新周期 day=10 变更应成功: %v", err)
	}
	lines2, total2 := e.Settle()
	logLines(t, "第二周期结算:", lines2)
	t.Logf("输出: total=%d", total2)
	if got := sumLines(lines2); got != total2 {
		t.Fatalf("第二周期结算总额 %d != 行之和 %d", total2, got)
	}
}

// TestConstructorRejections 构造时 D 越界、价格越界、初始套餐未知均须拒绝。
func TestConstructorRejections(t *testing.T) {
	cases := []struct {
		name    string
		days    int
		prices  map[string]int64
		initial string
		want    error
	}{
		{"D=0", 0, testPrices, "basic", ErrInvalidDays},
		{"D=367", 367, testPrices, "basic", ErrInvalidDays},
		{"价格为负", 30, map[string]int64{"a": -1}, "a", ErrInvalidPrice},
		{"价格超上限", 30, map[string]int64{"a": MaxPrice + 1}, "a", ErrInvalidPrice},
		{"初始套餐未知", 30, testPrices, "ghost", ErrUnknownPlan},
	}
	for _, c := range cases {
		_, err := NewEngine(c.days, c.prices, c.initial)
		t.Logf("输入: %s -> 输出: %v", c.name, err)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, 期望 %v", c.name, err, c.want)
		}
	}
	// 边界合法值应通过。
	if _, err := NewEngine(MinDays, map[string]int64{"a": 0}, "a"); err != nil {
		t.Fatalf("D=1 price=0 应合法: %v", err)
	}
	if _, err := NewEngine(MaxDays, map[string]int64{"a": MaxPrice}, "a"); err != nil {
		t.Fatalf("D=366 price=1e12 应合法: %v", err)
	}
}

// TestChangeRejections 变更拒绝按固定顺序只报第一个，且被拒绝后状态不变。
func TestChangeRejections(t *testing.T) {
	e := mustEngine(t, 30, "basic")
	if _, _, err := e.ChangePlan(5, "pro"); err != nil {
		t.Fatalf("首次变更失败: %v", err)
	}
	before := e.Lines()

	cases := []struct {
		name string
		day  int
		plan string
		want error
	}{
		{"未知套餐优先于同日", 5, "ghost", ErrUnknownPlan},
		{"与当前相同优先于day越界", 99, "pro", ErrSamePlan},
		{"day=0 越界", 0, "elite", ErrInvalidDay},
		{"day=D 越界", 30, "elite", ErrInvalidDay},
		{"day等于上次变更日", 5, "elite", ErrSameDayChange},
		{"day小于上次变更日", 3, "elite", ErrOutOfOrderDay},
	}
	for _, c := range cases {
		_, _, err := e.ChangePlan(c.day, c.plan)
		t.Logf("输入: ChangePlan(%d, %q) -> 输出: %v (判定: %v)", c.day, c.plan, err, c.want)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, 期望 %v", c.name, err, c.want)
		}
	}

	// 被拒绝的操作不得改变套餐、变更日与已生成的行。
	after := e.Lines()
	if len(after) != len(before) {
		t.Fatalf("拒绝后行数变化: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("拒绝后行 %d 变化: %+v -> %+v", i, before[i], after[i])
		}
	}
	if e.CurrentPlan() != "pro" {
		t.Fatalf("拒绝后套餐变化: %s", e.CurrentPlan())
	}
	// 上一次变更日仍为 5：day=5 仍报同日，day=6 仍可成功。
	if _, _, err := e.ChangePlan(5, "elite"); !errors.Is(err, ErrSameDayChange) {
		t.Fatalf("拒绝后上次变更日丢失: %v", err)
	}
	if _, _, err := e.ChangePlan(6, "elite"); err != nil {
		t.Fatalf("拒绝后合法变更失败: %v", err)
	}
	t.Logf("判定: 全部拒绝后行/套餐/上次变更日均未变, 后续合法变更正常")
}

// op 是一条待重放的操作：change 或 settle。
type op struct {
	change bool
	day    int
	plan   string
}

// runScript 在 Engine 上重放操作序列，返回每周期结算的行与总额。
func runScript(t *testing.T, e *Engine, script []op) ([][]Line, []int64) {
	t.Helper()
	var allLines [][]Line
	var totals []int64
	for i, o := range script {
		if o.change {
			_, _, err := e.ChangePlan(o.day, o.plan)
			t.Logf("重放[%d] ChangePlan(%d, %q) -> err=%v", i, o.day, o.plan, err)
		} else {
			lines, total := e.Settle()
			t.Logf("重放[%d] Settle -> total=%d lines=%d", i, total, len(lines))
			allLines = append(allLines, lines)
			totals = append(totals, total)
		}
	}
	return allLines, totals
}

// TestReplayMatchesNaive 与朴素实现逐行对照，且相同序列重放逐行相同。
func TestReplayMatchesNaive(t *testing.T) {
	days := 30
	script := []op{
		{change: true, day: 3, plan: "pro"},
		{change: true, day: 3, plan: "elite"}, // 同日拒绝
		{change: true, day: 2, plan: "elite"}, // 早于上次拒绝
		{change: true, day: 7, plan: "elite"},
		{change: true, day: 29, plan: "free"},
		{}, // settle 周期 1
		{change: true, day: 1, plan: "basic"},
		{change: true, day: 15, plan: "pro"},
		{}, // settle 周期 2
	}

	// 朴素实现逐步对照。
	n := newNaive(days, testPrices, "basic")
	e := mustEngine(t, days, "basic")
	for i, o := range script {
		if o.change {
			_, _, errE := e.ChangePlan(o.day, o.plan)
			errN := n.change(o.day, o.plan)
			t.Logf("步骤[%d] ChangePlan(%d, %q): engine=%v naive=%v", i, o.day, o.plan, errE, errN)
			if (errE == nil) != (errN == nil) {
				t.Fatalf("步骤[%d] 错误不一致: engine=%v naive=%v", i, errE, errN)
			}
			got, want := e.Lines(), n.lines
			if len(got) != len(want) {
				t.Fatalf("步骤[%d] 行数不一致: engine=%d naive=%d", i, len(got), len(want))
			}
			for j := range want {
				if got[j] != want[j] {
					t.Fatalf("步骤[%d] 行 %d 不一致: engine=%+v naive=%+v", i, j, got[j], want[j])
				}
			}
		} else {
			linesE, totalE := e.Settle()
			linesN, totalN := n.settle()
			t.Logf("步骤[%d] Settle: engine total=%d, naive total=%d", i, totalE, totalN)
			if totalE != totalN || len(linesE) != len(linesN) {
				t.Fatalf("步骤[%d] 结算不一致: engine=%d/%d naive=%d/%d",
					i, totalE, len(linesE), totalN, len(linesN))
			}
			for j := range linesN {
				if linesE[j] != linesN[j] {
					t.Fatalf("步骤[%d] 结算行 %d 不一致: engine=%+v naive=%+v", i, j, linesE[j], linesN[j])
				}
			}
		}
	}
	t.Logf("判定: 全部步骤与朴素公式重算逐行一致")

	// 相同操作序列重放：两个独立引擎逐行相同。
	e1 := mustEngine(t, days, "basic")
	e2 := mustEngine(t, days, "basic")
	lines1, totals1 := runScript(t, e1, script)
	lines2, totals2 := runScript(t, e2, script)
	if len(lines1) != len(lines2) {
		t.Fatalf("重放周期数不一致: %d vs %d", len(lines1), len(lines2))
	}
	for c := range lines1 {
		if totals1[c] != totals2[c] {
			t.Fatalf("重放周期 %d 总额不一致: %d vs %d", c, totals1[c], totals2[c])
		}
		for j := range lines1[c] {
			if lines1[c][j] != lines2[c][j] {
				t.Fatalf("重放周期 %d 行 %d 不一致: %+v vs %+v", c, j, lines1[c][j], lines2[c][j])
			}
		}
	}
	t.Logf("判定: 相同操作序列两次重放逐行相同")
}

// TestConcurrent 并发变更/结算/查询：无数据竞争，不变量始终成立。
func TestConcurrent(t *testing.T) {
	e := mustEngine(t, 366, "basic")
	plans := []string{"basic", "pro", "elite", "free"}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				switch i % 4 {
				case 0:
					_, _, _ = e.ChangePlan(1+(g*200+i)%365, plans[(g+i)%len(plans)])
				case 1:
					_, _ = e.Settle()
				case 2:
					lines := e.Lines()
					var sum int64
					for _, l := range lines {
						sum += l.Amount
					}
					_ = sum
				case 3:
					_ = e.Total()
					_ = e.CurrentPlan()
					_ = e.Cycle()
				}
			}
		}(g)
	}
	wg.Wait()

	// 收敛后校验不变量：行之和等于 Total，首行为预收，之后折退/补收成对。
	lines := e.Lines()
	if got := sumLines(lines); got != e.Total() {
		t.Fatalf("并发后行之和 %d != Total %d", got, e.Total())
	}
	if lines[0].Kind != Prepay || lines[0].Day != 0 {
		t.Fatalf("首行应为预收: %+v", lines[0])
	}
	for i := 1; i < len(lines); i += 2 {
		if lines[i].Kind != Refund || lines[i+1].Kind != Surcharge || lines[i].Day != lines[i+1].Day {
			t.Fatalf("行 %d/%d 未成对: %+v %+v", i, i+1, lines[i], lines[i+1])
		}
	}
	t.Logf("判定: 并发收敛后 %d 行, 行之和=%d 等于 Total, 行序为 预收+(折退,补收)*", len(lines), e.Total())
}
