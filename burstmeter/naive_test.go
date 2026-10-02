package burstmeter

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// naiveMeter 是严格按题目规则逐步书写的独立朴素参考实现，
// 不复用被测 Meter 的任何辅助逻辑，用于差分对照。
type naiveMeter struct {
	n, b, cmax, l0, w, price, dmax int64
	bal, lb                        int64
	batches                        []Batch
	mode                           int
	next                           int64

	earned, fromBal, repaid, dropped, throttled, feeTotal int64
}

func newNaive(c Config) *naiveMeter {
	return &naiveMeter{
		n: c.N, b: c.B, cmax: c.Cmax, l0: c.L, w: c.W,
		price: c.Price, dmax: c.Dmax, lb: c.L, mode: c.InitialMode,
	}
}

func (nm *naiveMeter) debt() int64 {
	var sum int64
	for _, q := range nm.batches {
		sum += q.Remaining
	}
	return sum
}

// tick 返回 (结果, 拒绝原因)；拒绝时状态不变。
func (nm *naiveMeter) tick(m int64, u int) (TickResult, RejectReason) {
	if m < 0 || u < 0 || u > 100 {
		return TickResult{}, ReasonInvalidArgument
	}
	if m < nm.next {
		return TickResult{}, ReasonSequenceFallback
	}
	if m > nm.next {
		return TickResult{}, ReasonSequenceGap
	}

	var minuteFee, minuteDrop, minuteThrottle int64

	// 一、到期计费：m-产生分钟 >= W 即到期。
	alive := []Batch{}
	for _, q := range nm.batches {
		if m-q.Minute >= nm.w {
			minuteFee += q.Remaining * nm.price
		} else {
			alive = append(alive, q)
		}
	}
	nm.batches = alive

	// 二、入账 e=n*b：FIFO 先偿最旧批次，剩余进 bal。
	e := nm.n * nm.b
	nm.earned += e
	rest := e
	for i := range nm.batches {
		if rest == 0 {
			break
		}
		pay := rest
		if pay > nm.batches[i].Remaining {
			pay = nm.batches[i].Remaining
		}
		nm.batches[i].Remaining -= pay
		rest -= pay
		nm.repaid += pay
	}
	pruned := []Batch{}
	for _, q := range nm.batches {
		if q.Remaining > 0 {
			pruned = append(pruned, q)
		}
	}
	nm.batches = pruned
	nm.bal += rest

	// 三、消耗 x=n*u：先 lb 后 bal；不足记限速/欠额。
	x := nm.n * int64(u)
	useLB := x
	if useLB > nm.lb {
		useLB = nm.lb
	}
	nm.lb -= useLB
	need := x - useLB
	useBal := need
	if useBal > nm.bal {
		useBal = nm.bal
	}
	nm.bal -= useBal
	nm.fromBal += useBal
	need -= useBal

	if need > 0 {
		if nm.mode == ModeInfinite {
			room := nm.dmax - nm.debt()
			borrow := need
			if borrow > room {
				borrow = room
			}
			if borrow < 0 {
				borrow = 0
			}
			if borrow > 0 {
				nm.batches = append(nm.batches, Batch{Minute: m, Remaining: borrow})
			}
			minuteThrottle = need - borrow
		} else {
			minuteThrottle = need
		}
	}
	nm.throttled += minuteThrottle

	// 四、封顶（消耗之后）。
	if nm.bal > nm.cmax {
		minuteDrop = nm.bal - nm.cmax
		nm.bal = nm.cmax
		nm.dropped += minuteDrop
	}

	nm.feeTotal += minuteFee
	nm.next = m + 1
	return TickResult{
		Fee:       minuteFee,
		Balance:   nm.bal,
		Debt:      nm.debt(),
		Dropped:   minuteDrop,
		Throttled: minuteThrottle,
	}, ReasonNone
}

func (nm *naiveMeter) setMode(mode int) (int64, RejectReason) {
	if mode != ModeStandard && mode != ModeInfinite {
		return 0, ReasonInvalidArgument
	}
	if mode == nm.mode {
		return 0, ReasonNone
	}
	var fee int64
	if nm.mode == ModeInfinite && mode == ModeStandard {
		fee = nm.debt() * nm.price
		nm.feeTotal += fee
		nm.batches = []Batch{}
	}
	nm.mode = mode
	return fee, ReasonNone
}

type opKind int

const (
	opTick opKind = iota
	opSetMode
)

type refOp struct {
	kind opKind
	m    int64
	u    int
	mode int
}

func randomConfig(rng *rand.Rand) Config {
	c := Config{
		N:           int64(rng.Intn(8) + 1),
		B:           int64(rng.Intn(100) + 1),
		Cmax:        int64(rng.Intn(201)),
		W:           int64(rng.Intn(6) + 1),
		Price:       int64(rng.Intn(6)),
		Dmax:        int64(rng.Intn(101)),
		InitialMode: rng.Intn(2),
	}
	c.L = int64(rng.Intn(int(c.Cmax) + 1))
	return c
}

// TestRandomAgainstNaive 用 2000 组随机操作序列与朴素模拟逐步差分对照，
// 并打印每组输入、终态输出与判定依据（-v 可见；-verbose 额外逐条打印）。
func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := randomConfig(rng)
		real, err := New(cfg)
		if err != nil {
			t.Fatalf("seed=%d 构造失败: %v", seed, err)
		}
		ref := newNaive(cfg)

		length := 20 + rng.Intn(21)
		var ops []refOp
		var inputs []string
		nextM := int64(0)
		for i := 0; i < length; i++ {
			if rng.Intn(10) < 2 {
				mode := rng.Intn(2)
				if rng.Intn(10) == 0 {
					mode = 2 + rng.Intn(2)
				}
				ops = append(ops, refOp{kind: opSetMode, mode: mode})
				inputs = append(inputs, fmt.Sprintf("Mode(%d)", mode))
				continue
			}
			m, u := nextM, rng.Intn(101)
			switch rng.Intn(10) {
			case 0:
				if nextM > 0 {
					m = rng.Int63n(nextM) // 回退
				}
			case 1:
				m = nextM + int64(rng.Intn(3)+1) // 缺口
			case 2:
				u = 101 + rng.Intn(5) // 非法利用率（参数非法优先）
			case 3:
				m = -int64(1 + rng.Intn(3)) // 负分钟
			}
			ops = append(ops, refOp{kind: opTick, m: m, u: u})
			inputs = append(inputs, fmt.Sprintf("T(%d,%d)", m, u))
			if m == nextM && u >= 0 && u <= 100 {
				nextM = m + 1
			}
		}

		for i, op := range ops {
			switch op.kind {
			case opTick:
				got, gotErr := real.Tick(op.m, op.u)
				want, wantReason := ref.tick(op.m, op.u)
				basis := diffTick(got, reasonOf(gotErr), want, wantReason)
				logf(t, "seed=%d #%d Tick(m=%d,u=%d) 实测{%+v}/%s 朴素{%+v}/%s 判定:%s",
					seed, i, op.m, op.u, got, reasonName(reasonOf(gotErr)), want, reasonName(wantReason), basis)
				if basis != "OK" {
					t.Fatalf("seed=%d #%d %s: %s\n输入=%s\ncfg=%+v",
						seed, i, inputs[i], basis, strings.Join(inputs, " "), cfg)
				}
			case opSetMode:
				gotFee, gotErr := real.SetMode(op.mode)
				wantFee, wantReason := ref.setMode(op.mode)
				basis := "OK"
				if reasonOf(gotErr) != wantReason {
					basis = fmt.Sprintf("拒绝原因 实测=%s 朴素=%s", reasonName(reasonOf(gotErr)), reasonName(wantReason))
				} else if gotFee != wantFee {
					basis = fmt.Sprintf("切换费用 实测=%d 朴素=%d", gotFee, wantFee)
				}
				logf(t, "seed=%d #%d SetMode(%d) 实测fee=%d/%s 朴素fee=%d/%s 判定:%s",
					seed, i, op.mode, gotFee, reasonName(reasonOf(gotErr)), wantFee, reasonName(wantReason), basis)
				if basis != "OK" {
					t.Fatalf("seed=%d #%d %s: %s", seed, i, inputs[i], basis)
				}
			}
			if !statesAgree(real, ref) {
				t.Fatalf("seed=%d #%d 状态发散:\n实测 bal=%d lb=%d debt=%d next=%d mode=%d batches=%+v\n朴素 bal=%d lb=%d debt=%d next=%d mode=%d batches=%+v\n输入=%s\ncfg=%+v",
					seed, i,
					real.Balance(), real.LaunchBalance(), real.Debt(), real.Next(), real.Mode(), real.Batches(),
					ref.bal, ref.lb, ref.debt(), ref.next, ref.mode, ref.batches,
					strings.Join(inputs, " "), cfg)
			}
			invariantCheck(t, seed, i, real, cfg)
		}

		gEarned, gCons, gRepay, gDrop, gThrottle, gFee := real.Totals()
		fromBal := gCons - (cfg.L - real.LaunchBalance())
		if gEarned != ref.earned || fromBal != ref.fromBal || gRepay != ref.repaid ||
			gDrop != ref.dropped || gThrottle != ref.throttled || gFee != ref.feeTotal {
			t.Fatalf("seed=%d 累计量发散: 实测 earned=%d fromBal=%d repaid=%d dropped=%d throttled=%d fee=%d；朴素 %d/%d/%d/%d/%d/%d",
				seed, gEarned, fromBal, gRepay, gDrop, gThrottle, gFee,
				ref.earned, ref.fromBal, ref.repaid, ref.dropped, ref.throttled, ref.feeTotal)
		}
		t.Logf("seed=%d 输入[%s] 终态 bal=%d lb=%d debt=%d fee=%d drop=%d throttle=%d 判定:与朴素模拟逐步一致",
			seed, strings.Join(inputs, " "),
			real.Balance(), real.LaunchBalance(), real.Debt(), gFee, gDrop, gThrottle)
	}
}

func diffTick(got TickResult, gotReason RejectReason, want TickResult, wantReason RejectReason) string {
	if gotReason != wantReason {
		return fmt.Sprintf("拒绝原因 实测=%s 朴素=%s", reasonName(gotReason), reasonName(wantReason))
	}
	if gotReason != ReasonNone {
		return "OK"
	}
	if got != want {
		return fmt.Sprintf("返回值 实测=%+v 朴素=%+v", got, want)
	}
	return "OK"
}

func reasonName(r RejectReason) string {
	switch r {
	case ReasonInvalidArgument:
		return "参数非法"
	case ReasonSequenceFallback:
		return "序号回退"
	case ReasonSequenceGap:
		return "序号缺口"
	default:
		return "接受"
	}
}

func statesAgree(real *Meter, ref *naiveMeter) bool {
	if real.Balance() != ref.bal || real.LaunchBalance() != ref.lb ||
		real.Debt() != ref.debt() || real.Next() != ref.next || real.Mode() != ref.mode {
		return false
	}
	return batchesEqual(real.Batches(), ref.batches)
}

func invariantCheck(t *testing.T, seed int64, opIdx int, m *Meter, cfg Config) {
	t.Helper()
	bal, lb, debt := m.Balance(), m.LaunchBalance(), m.Debt()
	if bal < 0 || bal > cfg.Cmax {
		t.Fatalf("seed=%d op=%d bal=%d 越界 [0,%d]", seed, opIdx, bal, cfg.Cmax)
	}
	if lb < 0 || lb > cfg.L {
		t.Fatalf("seed=%d op=%d lb=%d 非法（初值 L=%d，只减不增）", seed, opIdx, lb, cfg.L)
	}
	if debt < 0 || debt > cfg.Dmax {
		t.Fatalf("seed=%d op=%d debt=%d 越界 [0,%d]", seed, opIdx, debt, cfg.Dmax)
	}
	if m.Mode() == ModeStandard && len(m.Batches()) != 0 {
		t.Fatalf("seed=%d op=%d 标准模式下批次非空: %+v", seed, opIdx, m.Batches())
	}
	batches := m.Batches()
	var sum int64
	lastMinute := int64(-1)
	for j, q := range batches {
		if q.Remaining <= 0 {
			t.Fatalf("seed=%d op=%d 批次剩余非正: %+v", seed, opIdx, q)
		}
		if j > 0 && q.Minute <= lastMinute {
			t.Fatalf("seed=%d op=%d 批次产生分钟未严格递增: %+v", seed, opIdx, batches)
		}
		lastMinute = q.Minute
		sum += q.Remaining
	}
	if sum != debt {
		t.Fatalf("seed=%d op=%d 批次之和 %d != debt %d", seed, opIdx, sum, debt)
	}

	earned, consumed, repaid, dropped, _, _ := m.Totals()
	fromLB := cfg.L - lb
	fromBal := consumed - fromLB
	if earned != (bal-0)+fromBal+repaid+dropped {
		t.Fatalf("seed=%d op=%d 入账守恒被破坏: earned=%d != Δbal(%d)+bal支付(%d)+偿还(%d)+丢弃(%d)",
			seed, opIdx, earned, bal, fromBal, repaid, dropped)
	}
}
