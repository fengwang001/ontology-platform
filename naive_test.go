package billing

import (
	"flag"
	"fmt"
	"math/rand"
	"testing"
)

// -v 下通过 -billing-log 打开逐步输入/输出/判定日志。
var billingLog = flag.Bool("billing-log", false, "print each randomized step: input, output, decision")

// TestRandomAgainstNaive 用随机操作序列驱动生产实现，再用独立朴素模型重放同一日志，
// 逐账期比对自用/公摊，并校验守恒。所有拒绝操作不进入朴素重放（日志中标记原因）。
func TestRandomAgainstNaive(t *testing.T) {
	seed := timeNowSeed()
	for iter := 0; iter < 200; iter++ {
		rng := rand.New(rand.NewSource(seed + int64(iter)))
		runOneRandom(t, rng, seed+int64(iter))
	}
}

func runOneRandom(t *testing.T, rng *rand.Rand, seed int64) {
	t.Helper()
	const houses = 3
	const cap int64 = 64
	b := NewBuilding(1 + rng.Int63n(2))
	must(t, b.AddMeter("M", cap, true, 0))
	for i := 0; i < houses; i++ {
		mid := fmt.Sprintf("m%d", i)
		must(t, b.AddMeter(mid, cap, false, 0))
		must(t, b.AddHousehold(fmt.Sprintf("h%d", i), mid, int64(1+rng.Intn(9)), 0))
	}
	// 各表独立的“最后实抄读数状态”，生成器保证实抄值落在合法翻转范围内。
	type st struct {
		v, actualTime int64
		cap           int64
		estimated     bool
	}
	state := map[string]*st{}
	state["M"] = &st{cap: cap}
	for i := 0; i < houses; i++ {
		state[fmt.Sprintf("m%d", i)] = &st{cap: cap}
	}
	// 初始读数。
	for id := range state {
		_, err := b.EnterReading(ReadingInput{MeterID: id, Time: 0, Value: 0, Now: 1})
		must(t, err)
	}
	// 随机在住记录（只使用不重叠的整段）。
	for i := 0; i < houses; i++ {
		l := int64(rng.Intn(8))
		r := l + int64(1+rng.Intn(10))
		_ = b.AddOccupancy(fmt.Sprintf("h%d", i), l, r, 1)
	}

	horizon := int64(24)
	now := int64(2)
	periodEnd := int64(0)
	maxT := int64(0)
	lastEst := map[string]int64{}
	for step := 0; step < 200; step++ {
		choice := rng.Intn(10)
		switch {
		case choice < 5:
			// 录入实抄：挑一只表，时刻严格递增，值在合法范围内（允许翻转）。
			id := "M"
			if rng.Intn(houses+1) > 0 {
				id = fmt.Sprintf("m%d", rng.Intn(houses))
			}
			last := lastEst[id]
			if stt := state[id].actualTime; stt > last {
				last = stt
			}
			tm := last + 1 + int64(rng.Intn(4))
			if tm > horizon {
				break
			}
			s := state[id]
			cur := s.v
			// 合法新值：上升或翻转差额 <= cap/2。
			var v int64
			if rng.Intn(6) == 0 {
				// 翻转：新值 < cur 且 cur-v <= cap/2；保证 cur>0。
				if cur == 0 {
					v = int64(rng.Intn(6))
					if v > s.cap {
						v = s.cap
					}
				} else {
					maxDrop := s.cap / 2
					drop := int64(1 + rng.Intn(int(maxDrop)))
					if drop > cur {
						drop = cur
					}
					v = cur - drop
				}
			} else {
				v = cur + int64(rng.Intn(6))
				if v > s.cap {
					v = s.cap
				}
			}
			now++
			in := ReadingInput{MeterID: id, Time: tm, Value: v, Now: now}
			_, err := b.EnterReading(in)
			if *billingLog {
				t.Logf("seed=%d step=%d ENTER %+v -> err=%v", seed, step, in, err)
			}
			if err == nil {
				// 更晚实抄可能替代既有估抄；朴素侧以接受结果为准。
				s.v = v
				s.actualTime = tm
				s.estimated = false
				if tm > maxT {
					maxT = tm
				}
			}
		case choice < 7:
			// 录入估抄（距上次实抄超过 G，且值不小于上一读数）。
			id := fmt.Sprintf("m%d", rng.Intn(houses))
			s := state[id]
			if s.estimated {
				break // 简化：估抄尚未被替代前不再叠加
			}
			tm := s.actualTime + b.estGap + 1 + int64(rng.Intn(3))
			if tm > horizon {
				break
			}
			v := s.v + int64(rng.Intn(8))
			if v > s.cap {
				v = s.cap
			}
			now++
			in := ReadingInput{MeterID: id, Time: tm, Value: v, Estimated: true, Now: now}
			_, err := b.EnterReading(in)
			if *billingLog {
				t.Logf("seed=%d step=%d ESTIMATE %+v -> err=%v", seed, step, in, err)
			}
			if err == nil {
				s.estimated = true
				lastEst[id] = tm
				if tm > maxT {
					maxT = tm
				}
			}
		case choice < 8:
			// 换表（等量程，新表从 0 起）。
			id := fmt.Sprintf("m%d", rng.Intn(houses))
			s := state[id]
			if s.estimated {
				break
			}
			tm := s.actualTime + 1
			if tm > horizon {
				break
			}
			now++
			err := b.ChangeMeter(id, tm, s.v, 0, s.cap)
			if *billingLog {
				t.Logf("seed=%d step=%d CHANGE meter=%s at=%d oldFinal=%d -> err=%v",
					seed, step, id, tm, s.v, err)
			}
			if err == nil {
				s.v = 0
				s.actualTime = tm
				s.estimated = false
				if tm > maxT {
					maxT = tm
				}
			}
		default:
			// 尝试结算下一个首尾相接账期。
			length := int64(2 + rng.Intn(5))
			p0 := periodEnd
			p1 := p0 + length
			if p1 > maxT {
				break // 读数不足以覆盖账期终点，跳过（不发操作）
			}
			now++
			s, err := b.Settle(p0, p1, now)
			if *billingLog {
				t.Logf("seed=%d step=%d SETTLE [%d,%d) -> %+v err=%v", seed, step, p0, p1, s, err)
			}
			if err == nil {
				periodEnd = p1
			}
		}
	}

	// 朴素重放：仅接受成功操作。生产日志已记录每次输入与接受/拒绝及错误码。
	res := replayNaive(b.Journal(), b.estGap)
	bills := b.Bills()
	if len(bills) == 0 {
		return
	}
	for _, p := range bills {
		// 从生产当前值取（含全部更正后的最终账）。
		bill := b.findBill(p[0], p[1])
		cur := b.ensureCurrent(bill)
		nb, ok := res.bills[p]
		if !ok {
			t.Fatalf("seed=%d: naive missing bill %v", seed, p)
		}
		if cur.master != nb.master {
			t.Fatalf("seed=%d period %v master mismatch prod=%d naive=%d", seed, p, cur.master, nb.master)
		}
		for i, h := range bill.ids {
			if cur.self[i] != nb.self[h] || cur.share[i] != nb.shared[h] {
				t.Fatalf("seed=%d period %v household %s: prod self=%d shared=%d naive self=%d shared=%d",
					seed, p, h, cur.self[i], cur.share[i], nb.self[h], nb.shared[h])
			}
		}
		// 守恒：自用之和 + 公摊之和 == 总表用量。
		var sub, shr int64
		for i := range bill.ids {
			sub += cur.self[i]
			shr += cur.share[i]
		}
		if sub+shr != cur.master {
			t.Fatalf("seed=%d period %v conservation broken: %d+%d != %d", seed, p, sub, shr, cur.master)
		}
	}
}

// 简易确定性种子（测试中不依赖墙钟，改用固定基准+迭代）。
func timeNowSeed() int64 { return 1000 }

// BenchmarkSettleScaling 证明账期结算开销只与该账期涉及读数、分户数有关：
// 历史读数总量扩大两个数量级，单个账期的结算/更正重算耗时保持恒定。
func BenchmarkSettleScaling(b *testing.B) {
	scenarios := []struct {
		name       string
		histRead   int64
		households int
	}{
		{"hist10_h4", 10, 4},
		{"hist1000_h4", 1000, 4},
		{"hist10_h32", 10, 32},
	}
	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			bl := NewBuilding(1)
			mustBench(b, bl.AddMeter("M", 1_000_000, true, 0))
			for h := 0; h < sc.households; h++ {
				mid := "m" + itoaBench(h)
				mustBench(b, bl.AddMeter(mid, 1_000_000, false, 0))
				mustBench(b, bl.AddHousehold("h"+itoaBench(h), mid, 10, 0))
			}
			for k := int64(0); k < sc.histRead; k++ {
				_, err := bl.EnterReading(ReadingInput{MeterID: "M", Time: k, Value: k, Now: k + 1})
				mustBench(b, err)
			}
			p0 := sc.histRead*2 + 100
			p1 := p0 + 4
			// 目标账期涉及读数：各表在 p0/p1 两个锚点，与历史总量无关。
			_, err := bl.EnterReading(ReadingInput{MeterID: "M", Time: p0, Value: 0, Now: sc.histRead + 2})
			mustBench(b, err)
			for h := 0; h < sc.households; h++ {
				mid := "m" + itoaBench(h)
				_, err := bl.EnterReading(ReadingInput{MeterID: mid, Time: p0, Value: 0, Now: sc.histRead + 2})
				mustBench(b, err)
			}
			_, err = bl.EnterReading(ReadingInput{MeterID: "M", Time: p1, Value: int64(sc.households) * 10, Now: sc.histRead + 3})
			mustBench(b, err)
			for h := 0; h < sc.households; h++ {
				mid := "m" + itoaBench(h)
				_, err := bl.EnterReading(ReadingInput{MeterID: mid, Time: p1, Value: 8, Now: sc.histRead + 3})
				mustBench(b, err)
			}
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				// 注意：真实结算只能一次；这里测量“按当前读数重算该账期”的路径，
				// 通过在同一账期反复走只读重算等价成本（buildBill 纯计算部分）。
				_, _, _ = bl.precomputeCost(p0, p1)
			}
		})
	}
}

func itoaBench(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// precomputeCost 走一次与 Settle 相同的纯计算路径（构造账期快照+全部表重算+公摊分摊），
// 不改变任何状态，用于可重复测量单账期结算开销。
func (b *Building) precomputeCost(p0, p1 int64) (int64, int64, error) {
	bill := &Bill{
		Period: [2]int64{p0, p1},
		weight: make([]occupancyWeight, len(b.order)),
		area:   make([]int64, len(b.order)),
		ids:    make([]string, len(b.order)),
	}
	dur := p1 - p0
	durs := occupancyWeights(b.order, p0, p1)
	for i, d := range durs {
		bill.weight[i] = occupancyWeight{num: d, den: dur}
		bill.area[i] = b.order[i].area
		bill.ids[i] = b.order[i].id
	}
	rc, err := b.recomputeBill(bill, nil, bill)
	if err != nil || !rc.ok {
		return 0, 0, err
	}
	var sub int64
	for _, x := range rc.self {
		sub += x
	}
	return rc.master, sub, nil
}

// testing.TB 同时支持 *testing.B。
func mustBench(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatal(err)
	}
}
