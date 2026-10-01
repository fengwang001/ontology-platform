package fee

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"testing"
)

// naiveModel 是按题述规则逐步写成的朴素参照实现：
// 每次变更后都把当前账期全部有效成交从起点 C0 重新算一遍费用。
type naiveModel struct {
	th, ra []int64
	cap    int64
	rho    int64
	period int
	accts  map[string]*naiveAcct
	tids   map[string]*naiveTidRef
}

type naiveAcct struct {
	c0     int64
	trades []*naiveTrade
}

type naiveTrade struct {
	tid       string
	amt       int64
	fee       int64
	cancelled bool
}

type naiveTidRef struct {
	acct      string
	period    int
	cancelled bool
}

func newNaive(th, ra []int64, cap, rho int64) *naiveModel {
	return &naiveModel{
		th: th, ra: ra, cap: cap, rho: rho,
		accts: make(map[string]*naiveAcct),
		tids:  make(map[string]*naiveTidRef),
	}
}

func (n *naiveModel) phi(cum int64) int64 {
	var total int64
	for k, r := range n.ra {
		var lo int64
		if k > 0 {
			lo = n.th[k-1]
		}
		var seg int64
		if cum > lo {
			seg = cum - lo
			if k < len(n.th) && seg > n.th[k]-lo {
				seg = n.th[k] - lo
			}
		}
		total += seg * r / 10000
	}
	if total > n.cap {
		return n.cap
	}
	return total
}

func (n *naiveModel) recompute(a *naiveAcct) {
	cum := a.c0
	for _, tr := range a.trades {
		if tr.cancelled {
			continue
		}
		tr.fee = n.phi(cum+tr.amt) - n.phi(cum)
		cum += tr.amt
	}
}

func (n *naiveModel) trade(acct, tid string, amt int64) (int64, error) {
	if acct == "" || tid == "" || amt < 1 || amt > 1_000_000_000 {
		return 0, ErrInvalidParam
	}
	if _, ok := n.tids[tid]; ok {
		return 0, ErrDuplicateTID
	}
	a := n.accts[acct]
	if a == nil {
		a = &naiveAcct{}
		n.accts[acct] = a
	}
	var sum int64
	for _, tr := range a.trades {
		if !tr.cancelled {
			sum += tr.amt
		}
	}
	if a.c0+sum+amt > 100_000_000_000_000 {
		return 0, ErrCumulativeLimit
	}
	a.trades = append(a.trades, &naiveTrade{tid: tid, amt: amt})
	n.tids[tid] = &naiveTidRef{acct: acct, period: n.period}
	n.recompute(a)
	return a.trades[len(a.trades)-1].fee, nil
}

func (n *naiveModel) cancel(tid string) (int64, []FeeChange, error) {
	ref, ok := n.tids[tid]
	if !ok {
		return 0, nil, ErrTradeNotFound
	}
	if ref.cancelled {
		return 0, nil, ErrAlreadyCancelled
	}
	if ref.period != n.period {
		return 0, nil, ErrPeriodClosed
	}
	ref.cancelled = true
	a := n.accts[ref.acct]
	oldFees := make(map[string]int64, len(a.trades))
	var old int64
	idx := -1
	for i, tr := range a.trades {
		oldFees[tr.tid] = tr.fee
		if tr.tid == tid {
			tr.cancelled = true
			old = tr.fee
			idx = i
		}
	}
	n.recompute(a)
	var changes []FeeChange
	for _, tr := range a.trades[idx+1:] {
		if tr.cancelled {
			continue
		}
		if tr.fee != oldFees[tr.tid] {
			changes = append(changes, FeeChange{TID: tr.tid, OldFee: oldFees[tr.tid], NewFee: tr.fee})
		}
	}
	return old, changes, nil
}

func (n *naiveModel) nextPeriod() {
	for _, a := range n.accts {
		var sum int64
		for _, tr := range a.trades {
			if !tr.cancelled {
				sum += tr.amt
			}
		}
		a.c0 = (a.c0 + sum) * n.rho / 10000
		a.trades = nil
	}
	n.period++
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) && errors.Is(b, a)
}

func sameChanges(a, b []FeeChange) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// compareState 全量比对两个模型的账期、各账户 C0 与当前账期成交费用。
func compareState(t *testing.T, seq, op int, real *Calculator, naive *naiveModel) {
	t.Helper()
	if real.period != naive.period {
		t.Fatalf("序列 %d 操作 %d: 账期不一致: 实现=%d 朴素=%d", seq, op, real.period, naive.period)
	}
	names := make(map[string]bool)
	for name := range real.accounts {
		names[name] = true
	}
	for name := range naive.accts {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		ra := real.accounts[name]
		na := naive.accts[name]
		if (ra == nil) != (na == nil) {
			t.Fatalf("序列 %d 操作 %d: 账户 %q 存在性不一致", seq, op, name)
		}
		if ra == nil {
			continue
		}
		if ra.c0 != na.c0 {
			t.Fatalf("序列 %d 操作 %d: 账户 %q C0 不一致: 实现=%d 朴素=%d", seq, op, name, ra.c0, na.c0)
		}
		if len(ra.trades) != len(na.trades) {
			t.Fatalf("序列 %d 操作 %d: 账户 %q 成交数不一致: 实现=%d 朴素=%d", seq, op, name, len(ra.trades), len(na.trades))
		}
		for i, tr := range ra.trades {
			nt := na.trades[i]
			if tr.tid != nt.tid || tr.amt != nt.amt || tr.fee != nt.fee || tr.cancelled != nt.cancelled {
				t.Fatalf("序列 %d 操作 %d: 账户 %q 第 %d 笔不一致: 实现=%+v 朴素=%+v",
					seq, op, name, i, TradeRecord{TID: tr.tid, Amt: tr.amt, Fee: tr.fee, Cancelled: tr.cancelled}, *nt)
			}
		}
	}
}

// 2000 组随机操作序列与朴素重算模型逐步对照，日志打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewPCG(1135, 2026))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		m := 1 + rng.IntN(4)
		th := make([]int64, m)
		prev := int64(0)
		for i := range th {
			prev += 1 + rng.Int64N(2000)
			th[i] = prev
		}
		ra := make([]int64, m+1)
		for i := range ra {
			if rng.IntN(10) == 0 {
				ra[i] = rng.Int64N(10001)
			} else {
				ra[i] = rng.Int64N(30)
			}
		}
		var capV int64
		switch rng.IntN(4) {
		case 0:
			capV = 0
		case 1:
			capV = rng.Int64N(50)
		case 2:
			capV = rng.Int64N(1000)
		default:
			capV = 100_000_000_000_000
		}
		var rho int64
		switch rng.IntN(4) {
		case 0:
			rho = 0
		case 1:
			rho = 10000
		default:
			rho = rng.Int64N(10001)
		}

		real, err := NewCalculator(th, ra, capV, rho)
		if err != nil {
			t.Fatalf("序列 %d: 合法参数被拒绝: %v", seq, err)
		}
		naive := newNaive(th, ra, capV, rho)
		t.Logf("序列 %d 输入: 阈值=%v 费率=%v CAP=%d ρ=%d", seq, th, ra, capV, rho)

		var tids []string
		accts := []string{"a", "b", "c"}
		ops := 20 + rng.IntN(30)
		for op := 0; op < ops; op++ {
			kind := rng.IntN(100)
			switch {
			case kind < 55:
				acct := accts[rng.IntN(len(accts))]
				if rng.IntN(20) == 0 {
					acct = ""
				}
				tid := fmt.Sprintf("s%d-t%d", seq, len(tids))
				fresh := true
				if rng.IntN(10) == 0 && len(tids) > 0 {
					tid = tids[rng.IntN(len(tids))]
					fresh = false
				}
				amt := int64(1 + rng.Int64N(3000))
				switch rng.IntN(20) {
				case 0:
					amt = 0
				case 1:
					amt = -3
				case 2:
					amt = 1_000_000_001
				}
				feeReal, errReal := real.Trade(acct, tid, amt)
				feeNaive, errNaive := naive.trade(acct, tid, amt)
				t.Logf("序列 %d 操作 %d 输入: Trade(%q, %q, %d) 输出: 实现=(fee=%d, err=%v) 朴素=(fee=%d, err=%v) 判定: 错误类别一致且成功时费用相等",
					seq, op, acct, tid, amt, feeReal, errReal, feeNaive, errNaive)
				if !sameErr(errReal, errNaive) {
					t.Fatalf("序列 %d 操作 %d: Trade 错误不一致: 实现=%v 朴素=%v", seq, op, errReal, errNaive)
				}
				if errReal == nil && feeReal != feeNaive {
					t.Fatalf("序列 %d 操作 %d: Trade 费用不一致: 实现=%d 朴素=%d", seq, op, feeReal, feeNaive)
				}
				if errReal == nil && fresh {
					tids = append(tids, tid)
				}
			case kind < 85:
				tid := "ghost"
				if len(tids) > 0 && rng.IntN(5) > 0 {
					tid = tids[rng.IntN(len(tids))]
				}
				oldReal, chReal, errReal := real.Cancel(tid)
				oldNaive, chNaive, errNaive := naive.cancel(tid)
				t.Logf("序列 %d 操作 %d 输入: Cancel(%q) 输出: 实现=(old=%d, changes=%+v, err=%v) 朴素=(old=%d, changes=%+v, err=%v) 判定: 错误类别、被撤销费用与变更列表一致",
					seq, op, tid, oldReal, chReal, errReal, oldNaive, chNaive, errNaive)
				if !sameErr(errReal, errNaive) {
					t.Fatalf("序列 %d 操作 %d: Cancel 错误不一致: 实现=%v 朴素=%v", seq, op, errReal, errNaive)
				}
				if errReal == nil {
					if oldReal != oldNaive {
						t.Fatalf("序列 %d 操作 %d: 被撤销费用不一致: 实现=%d 朴素=%d", seq, op, oldReal, oldNaive)
					}
					if !sameChanges(chReal, chNaive) {
						t.Fatalf("序列 %d 操作 %d: 变更列表不一致: 实现=%+v 朴素=%+v", seq, op, chReal, chNaive)
					}
				}
			default:
				real.NextPeriod()
				naive.nextPeriod()
				t.Logf("序列 %d 操作 %d 输入: NextPeriod() 输出: 账期=%d 判定: 各账户 C0 按 floor((C0+有效金额)*ρ/10000) 结转后一致",
					seq, op, real.Period())
			}
			compareState(t, seq, op, real, naive)
		}
		t.Logf("序列 %d 判定通过: %d 个操作后两模型账期、C0、各笔费用完全一致", seq, ops)
	}
}
