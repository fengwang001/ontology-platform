package interest

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

// naive 是题目语义的朴素实现：按日索引保存存取与利率，
// 每次计息都逐日循环重算，用于对照被测实现。
type naive struct {
	start, m, prev, rem, balance int64
	flow                         []int64 // flow[day] = 当日存取与冲正净额
	rset                         []int64 // rset[day] = 当日设定的利率，未设定为 -1
	recs                         []Record
}

func newNaive(start int64) *naive {
	return &naive{start: start, m: start, prev: start, rset: []int64{}}
}

func (n *naive) grow(day int64) {
	for int64(len(n.flow)) <= day {
		n.flow = append(n.flow, 0)
	}
	for int64(len(n.rset)) <= day {
		n.rset = append(n.rset, -1)
	}
}

// intByDay 返回每日贷记利息数组（按当前结息记录重算）。
func (n *naive) intByDay() []int64 {
	var out []int64
	for _, r := range n.recs {
		for int64(len(out)) <= r.Date {
			out = append(out, 0)
		}
		out[r.Date] += r.I
	}
	return out
}

// accumulate 逐日循环累计 [lo, hi] 上「日终余额 × 当日年利率」之和。
func (n *naive) accumulate(lo, hi int64) *big.Int {
	total := new(big.Int)
	intByDay := n.intByDay()
	var flowSum, intSum, rate int64
	for k := int64(0); k <= hi; k++ {
		if int(k) < len(n.flow) {
			flowSum += n.flow[k]
		}
		if int(k) < len(intByDay) {
			intSum += intByDay[k]
		}
		if int(k) < len(n.rset) && n.rset[k] >= 0 {
			rate = n.rset[k]
		}
		if k >= lo {
			t := big.NewInt(flowSum + intSum)
			t.Mul(t, big.NewInt(rate))
			total.Add(total, t)
		}
	}
	return total
}

func (n *naive) deposit(d, x int64) error {
	if e := checkDate(d); e != nil {
		return e
	}
	if x < 1 || x > maxAmount {
		return newError(ErrInvalidArg, "x 越界")
	}
	if d < n.m {
		return newError(ErrOutOfOrder, "时序倒退")
	}
	if n.balance+x > maxDepositBalance {
		return newError(ErrBalanceOverflow, "余额越限")
	}
	n.grow(d)
	n.flow[d] += x
	n.balance += x
	n.m = d
	return nil
}

func (n *naive) withdraw(d, x int64) error {
	if e := checkDate(d); e != nil {
		return e
	}
	if x < 1 || x > maxAmount {
		return newError(ErrInvalidArg, "x 越界")
	}
	if d < n.m {
		return newError(ErrOutOfOrder, "时序倒退")
	}
	if x > n.balance {
		return newError(ErrInsufficient, "余额不足")
	}
	n.grow(d)
	n.flow[d] -= x
	n.balance -= x
	n.m = d
	return nil
}

func (n *naive) setRate(d, r int64) error {
	if e := checkDate(d); e != nil {
		return e
	}
	if r < 0 || r > maxRate {
		return newError(ErrInvalidArg, "r 越界")
	}
	if d < n.m {
		return newError(ErrOutOfOrder, "时序倒退")
	}
	n.grow(d)
	n.rset[d] = r
	n.m = d
	return nil
}

func (n *naive) settle(d int64) (int64, int64, int64, error) {
	if e := checkDate(d); e != nil {
		return 0, 0, 0, e
	}
	if d-n.prev > maxSpan {
		return 0, 0, 0, newError(ErrInvalidArg, "覆盖天数超限")
	}
	if d < n.m {
		return 0, 0, 0, newError(ErrOutOfOrder, "时序倒退")
	}
	if d <= n.prev {
		return 0, 0, 0, newError(ErrEmptyInterval, "空区间")
	}
	N := n.accumulate(n.prev, d-1)
	N.Add(N, big.NewInt(n.rem))
	i, r := divmod(N)
	n.recs = append(n.recs, Record{Start: n.prev, Date: d, I: i, R: r})
	n.rem = r
	n.prev = d
	n.balance += i
	n.m = d
	return i, n.balance, n.rem, nil
}

func (n *naive) correct(d, delta int64) (int64, int64, []Change, error) {
	if e := checkDate(d); e != nil {
		return 0, 0, nil, e
	}
	if d < n.start {
		return 0, 0, nil, newError(ErrInvalidArg, "d 小于起始日")
	}
	if delta == 0 || delta > maxAmount || delta < -maxAmount {
		return 0, 0, nil, newError(ErrInvalidArg, "delta 越界")
	}
	if d >= n.prev {
		return 0, 0, nil, newError(ErrNotSettled, "区间未结息")
	}

	savedFlow := append([]int64(nil), n.flow...)
	savedRecs := append([]Record(nil), n.recs...)
	savedBalance, savedRem := n.balance, n.rem

	n.grow(d)
	n.flow[d] += delta

	// 从头逐条重算全部结息记录（结息日不大于 d 的记录输入不变，结果不变）。
	newRecs := make([]Record, len(n.recs))
	Rin := int64(0)
	for j, rec := range n.recs {
		// 只用前 j 条已重算的记录累计贷记利息
		old := n.recs
		n.recs = newRecs[:j]
		N := n.accumulate(rec.Start, rec.Date-1)
		n.recs = old
		N.Add(N, big.NewInt(Rin))
		i, r := divmod(N)
		newRecs[j] = Record{Start: rec.Start, Date: rec.Date, I: i, R: r}
		Rin = r
	}
	var changes []Change
	for j, rec := range newRecs {
		if rec.Date > d && (rec.I != n.recs[j].I || rec.R != n.recs[j].R) {
			changes = append(changes, Change{
				Date: rec.Date,
				OldI: n.recs[j].I, NewI: rec.I,
				OldR: n.recs[j].R, NewR: rec.R,
			})
		}
	}
	n.recs = newRecs

	// 逐日检查第 d 天到 m 的日终余额
	intByDay := n.intByDay()
	var bal int64
	neg, over := false, false
	for k := int64(0); k <= n.m; k++ {
		if int(k) < len(n.flow) {
			bal += n.flow[k]
		}
		if int(k) < len(intByDay) {
			bal += intByDay[k]
		}
		if k >= d {
			if bal < 0 {
				neg = true
			}
			if bal > maxBalance {
				over = true
			}
		}
	}
	if neg || over {
		n.flow = savedFlow
		n.recs = savedRecs
		n.balance, n.rem = savedBalance, savedRem
		if neg {
			return 0, 0, nil, newError(ErrCorrectNegative, "冲正后余额为负")
		}
		return 0, 0, nil, newError(ErrCorrectOverflow, "冲正后余额越限")
	}
	n.rem = n.recs[len(n.recs)-1].R
	var flowSum, intSum int64
	for _, v := range n.flow {
		flowSum += v
	}
	for _, r := range n.recs {
		intSum += r.I
	}
	n.balance = flowSum + intSum
	return n.balance, n.rem, changes, nil
}

// checkInvariants 校验两条守恒不变式，并返回判定依据供日志打印。
func (n *naive) checkInvariants(t *testing.T, a *Account, tag string) {
	t.Helper()
	// 余额恒等式：余额 = 存取冲正净额 + 已贷记利息
	var flowSum, intSum int64
	for _, v := range n.flow {
		flowSum += v
	}
	for _, r := range n.recs {
		intSum += r.I
	}
	if n.balance != flowSum+intSum {
		t.Fatalf("[%s] 余额恒等式不成立: balance=%d flow=%d interest=%d", tag, n.balance, flowSum, intSum)
	}
	if a.Balance() != n.balance {
		t.Fatalf("[%s] 余额不一致: account=%d naive=%d", tag, a.Balance(), n.balance)
	}
	if a.Remainder() != n.rem || a.Prev() != n.prev {
		t.Fatalf("[%s] 余数/prev 不一致: account=(%d,%d) naive=(%d,%d)",
			tag, a.Remainder(), a.Prev(), n.rem, n.prev)
	}
	recsA := a.Records()
	if len(recsA) != len(n.recs) || (len(recsA) > 0 && !reflect.DeepEqual(recsA, n.recs)) {
		t.Fatalf("[%s] 结息记录不一致: account=%+v naive=%+v", tag, a.Records(), n.recs)
	}
	// 余数范围
	if n.rem < 0 || n.rem >= D {
		t.Fatalf("[%s] 余数 %d 不在 [0, D) 内", tag, n.rem)
	}
	// 积数守恒：已结息总额 × D + R == 全部已结息区间按当前 B_k 计得的积数
	if len(n.recs) > 0 {
		total := n.accumulate(n.start, n.prev-1)
		lhs := big.NewInt(intSum)
		lhs.Mul(lhs, big.NewInt(D))
		lhs.Add(lhs, big.NewInt(n.rem))
		if lhs.Cmp(total) != 0 {
			t.Fatalf("[%s] 积数守恒不成立: 已结息总额×D+R=%s, 逐日积数=%s", tag, lhs, total)
		}
		t.Logf("[%s] 判定依据: 余额=%d=净额%d+利息%d; 积数守恒 %s = %d×D+%d",
			tag, n.balance, flowSum, intSum, total, intSum, n.rem)
	}
}

func errKind(err error) ErrKind {
	if err == nil {
		return 0
	}
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return -1
}

// TestRandomAgainstNaive 生成 2000 组随机操作序列，
// 与逐日循环重放全部历史的朴素模拟逐操作对照。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rnd := rand.New(rand.NewSource(int64(seq) * 2654435761))
		start := int64(rnd.Intn(51))
		a, err := New(start)
		if err != nil {
			t.Fatalf("seq %d: New(%d): %v", seq, start, err)
		}
		n := newNaive(start)
		cursor := start
		ops := 30 + rnd.Intn(40)
		for op := 0; op < ops; op++ {
			tag := func(name string) string {
				return fmt.Sprintf("seq=%d op=%d %s", seq, op, name)
			}
			// 日期：多数情况下向前推进，偶尔同日或倒退
			advance := func() int64 {
				switch rnd.Intn(10) {
				case 0, 1:
					// 同日
				case 2:
					cursor -= int64(rnd.Intn(4)) // 可能时序倒退
					if cursor < 0 {
						cursor = 0
					}
				default:
					cursor += int64(rnd.Intn(5))
				}
				return cursor
			}
			switch rnd.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14: // Deposit
				d := advance()
				x := int64(1 + rnd.Intn(200000))
				if rnd.Intn(20) == 0 {
					x = int64(1 + rnd.Int63n(2*maxAmount)) // 可能越界
				}
				errA := a.Deposit(d, x)
				errN := n.deposit(d, x)
				if errKind(errA) != errKind(errN) {
					t.Fatalf("%s: Deposit(%d,%d) account=%v naive=%v", tag("Deposit"), d, x, errA, errN)
				}
				t.Logf("[%s] Deposit(%d,%d) -> err=%v", tag("Deposit"), d, x, errA)
			case 15, 16, 17, 18, 19, 20, 21, 22, 23, 24: // Withdraw
				d := advance()
				x := int64(1 + rnd.Intn(200000))
				if rnd.Intn(10) == 0 {
					x = n.balance + int64(rnd.Intn(100)) // 可能余额不足
					if x < 1 {
						x = 1
					}
					if x > maxAmount {
						x = maxAmount
					}
				}
				errA := a.Withdraw(d, x)
				errN := n.withdraw(d, x)
				if errKind(errA) != errKind(errN) {
					t.Fatalf("%s: Withdraw(%d,%d) account=%v naive=%v", tag("Withdraw"), d, x, errA, errN)
				}
				t.Logf("[%s] Withdraw(%d,%d) -> err=%v", tag("Withdraw"), d, x, errA)
			case 25, 26, 27, 28, 29, 30, 31, 32: // SetRate
				d := advance()
				r := int64(rnd.Intn(731))
				if rnd.Intn(15) == 0 {
					r = int64(rnd.Intn(10002)) // 可能越界
				}
				errA := a.SetRate(d, r)
				errN := n.setRate(d, r)
				if errKind(errA) != errKind(errN) {
					t.Fatalf("%s: SetRate(%d,%d) account=%v naive=%v", tag("SetRate"), d, r, errA, errN)
				}
				t.Logf("[%s] SetRate(%d,%d) -> err=%v", tag("SetRate"), d, r, errA)
			case 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46: // Settle
				var d int64
				switch rnd.Intn(20) {
				case 0:
					d = n.prev + maxSpan - int64(rnd.Intn(3)) // 覆盖天数边界 3658..3660
				case 1:
					d = n.prev + maxSpan + 1 // 3661，参数非法
				case 2:
					d = n.prev // 空区间
				default:
					d = advance() + 1 + int64(rnd.Intn(15))
				}
				if d < 0 {
					d = 0
				}
				iA, bA, rA, errA := a.Settle(d)
				iN, bN, rN, errN := n.settle(d)
				if errKind(errA) != errKind(errN) {
					t.Fatalf("%s: Settle(%d) account=%v naive=%v", tag("Settle"), d, errA, errN)
				}
				if errA == nil && (iA != iN || bA != bN || rA != rN) {
					t.Fatalf("%s: Settle(%d) account=(%d,%d,%d) naive=(%d,%d,%d)",
						tag("Settle"), d, iA, bA, rA, iN, bN, rN)
				}
				t.Logf("[%s] Settle(%d) -> i=%d bal=%d R=%d err=%v", tag("Settle"), d, iN, bN, rN, errA)
				if errA == nil {
					cursor = d
				}
			case 47, 48, 49, 50, 51, 52, 53, 54, 55, 56: // Correct
				var d int64
				span := n.prev - n.start
				switch rnd.Intn(10) {
				case 0:
					d = n.prev + int64(rnd.Intn(3)) // 可能区间未结息
				case 1:
					d = n.start - 1 // 参数非法
				default:
					if span > 0 {
						d = n.start + int64(rnd.Intn(int(span)))
					} else {
						d = n.start
					}
				}
				delta := int64(1+rnd.Intn(300000)) * int64(2*rnd.Intn(2)-1)
				switch rnd.Intn(15) {
				case 0:
					delta = 0 // 参数非法
				case 1:
					delta = -n.balance - int64(rnd.Intn(10)) // 可能冲正后为负
				case 2:
					delta = int64(2 * rnd.Intn(2)) // 0 或 2
				}
				bA, rA, cA, errA := a.Correct(d, delta)
				bN, rN, cN, errN := n.correct(d, delta)
				if errKind(errA) != errKind(errN) {
					t.Fatalf("%s: Correct(%d,%d) account=%v naive=%v", tag("Correct"), d, delta, errA, errN)
				}
				if errA == nil && (bA != bN || rA != rN || !reflect.DeepEqual(cA, cN)) {
					t.Fatalf("%s: Correct(%d,%d) account=(%d,%d,%+v) naive=(%d,%d,%+v)",
						tag("Correct"), d, delta, bA, rA, cA, bN, rN, cN)
				}
				t.Logf("[%s] Correct(%d,%d) -> bal=%d R=%d changes=%+v err=%v",
					tag("Correct"), d, delta, bN, rN, cN, errA)
			default: // 其余为查询/不变式校验
			}
			n.checkInvariants(t, a, fmt.Sprintf("seq=%d op=%d", seq, op))
		}
	}
}
