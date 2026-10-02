package sla

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// naiveSim 是按分钟标记数组逐步写成的朴素模拟，用于对照。
type naiveSim struct {
	p        Params
	month    int64
	open     bool
	fee      int64
	faults   map[[2]int64]int
	excludes [][2]int64
	s        int64
	yearPaid map[int64]int64
}

func newNaive(p Params) *naiveSim {
	return &naiveSim{p: p, faults: map[[2]int64]int{}, yearPaid: map[int64]int64{}}
}

func (n *naiveSim) valid(a, b int64) bool { return 0 <= a && a < b && b <= n.p.L }

func (n *naiveSim) newMonth(fee int64) error {
	if fee < 1 || fee > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	if n.open {
		return ErrMonthOpen
	}
	n.open, n.fee = true, fee
	n.faults = map[[2]int64]int{}
	n.excludes = nil
	return nil
}

func (n *naiveSim) report(a, b int64) error {
	if !n.valid(a, b) {
		return ErrInvalidParam
	}
	if !n.open {
		return ErrNoOpenMonth
	}
	n.faults[[2]int64{a, b}]++
	return nil
}

// 朴素并集长度：按分钟逐格标记。
func (n *naiveSim) excludeUnionLen(extra [2]int64) int64 {
	mark := make([]bool, n.p.L)
	for _, e := range n.excludes {
		for m := e[0]; m < e[1]; m++ {
			mark[m] = true
		}
	}
	if extra[0] < extra[1] {
		for m := extra[0]; m < extra[1]; m++ {
			mark[m] = true
		}
	}
	var total int64
	for _, v := range mark {
		if v {
			total++
		}
	}
	return total
}

func (n *naiveSim) addExclude(a, b int64) error {
	if !n.valid(a, b) {
		return ErrInvalidParam
	}
	if !n.open {
		return ErrNoOpenMonth
	}
	if n.excludeUnionLen([2]int64{a, b}) > n.p.Ex {
		return ErrExclusionLimit
	}
	n.excludes = append(n.excludes, [2]int64{a, b})
	return nil
}

func (n *naiveSim) revoke(a, b int64) error {
	if !n.valid(a, b) {
		return ErrInvalidParam
	}
	if !n.open {
		return ErrNoOpenMonth
	}
	key := [2]int64{a, b}
	if n.faults[key] == 0 {
		return ErrIntervalNotFound
	}
	n.faults[key]--
	if n.faults[key] == 0 {
		delete(n.faults, key)
	}
	return nil
}

// 朴素结算：先按分钟标记故障，再按分钟扣除排除，最后按连续段统计并丢弃短段。
func (n *naiveSim) close() (Result, error) {
	if !n.open {
		return Result{}, ErrNoOpenMonth
	}
	faultMin := make([]bool, n.p.L)
	for iv, cnt := range n.faults {
		for k := 0; k < cnt; k++ {
			for m := iv[0]; m < iv[1]; m++ {
				faultMin[m] = true
			}
		}
	}
	exclMin := make([]bool, n.p.L)
	for _, e := range n.excludes {
		for m := e[0]; m < e[1]; m++ {
			exclMin[m] = true
		}
	}
	var d, run int64
	flush := func() {
		if run >= n.p.G {
			d += run
		}
		run = 0
	}
	for m := int64(0); m < n.p.L; m++ {
		if faultMin[m] && !exclMin[m] {
			run++
		} else {
			flush()
		}
	}
	flush()

	a := (n.p.L - d) * 1_000_000 / n.p.L
	var base int64
	switch {
	case a >= n.p.T1:
		base = 0
	case a >= n.p.T2:
		base = n.p.C1
	case a >= n.p.T3:
		base = n.p.C2
	default:
		base = n.p.C3
	}
	res := Result{D: d, A: a, Base: base}
	year := n.month / 12
	if base == 0 {
		n.s = 0
	} else {
		esc := n.s
		if esc > n.p.Sm {
			esc = n.p.Sm
		}
		pct := base + n.p.St*esc
		if pct > 100 {
			pct = 100
		}
		credit := (n.fee*pct + 99) / 100
		if remain := n.p.Y - n.yearPaid[year]; credit > remain {
			credit = remain
		}
		n.yearPaid[year] += credit
		n.s++
		res.Pct = pct
		res.Credit = credit
	}
	n.month++
	n.open = false
	return res, nil
}

func randParams(r *rand.Rand) Params {
	p := Params{}
	p.L = 1 + r.Int63n(40)
	p.G = r.Int63n(p.L + 1)
	// 三个严格递减的档位：1 <= T3 < T2 < T1 <= 1e6。
	t3 := 1 + r.Int63n(999_998)
	t2 := t3 + 1 + r.Int63n(999_999-t3)
	t1 := t2 + 1 + r.Int63n(1_000_000-t2)
	p.T1, p.T2, p.T3 = t1, t2, t3
	c1 := 1 + r.Int63n(98)
	c2 := c1 + 1 + r.Int63n(99-c1)
	c3 := c2 + 1 + r.Int63n(100-c2)
	p.C1, p.C2, p.C3 = c1, c2, c3
	p.St = r.Int63n(101)
	p.Sm = r.Int63n(13)
	switch r.Intn(3) {
	case 0:
		p.Y = 0
	case 1:
		p.Y = r.Int63n(2000)
	default:
		p.Y = r.Int63n(1_000_000_000_001)
	}
	p.Ex = r.Int63n(p.L + 1)
	return p
}

// randInterval 生成区间，约 1/8 概率为非法区间。
func randInterval(r *rand.Rand, L int64) (int64, int64) {
	if r.Intn(8) == 0 {
		switch r.Intn(4) {
		case 0:
			a := r.Int63n(L + 1)
			return a, a // 空区间
		case 1:
			return -1 - r.Int63n(3), r.Int63n(L+1) + 1 // 负端点
		case 2:
			return 0, L + 1 + r.Int63n(3) // 超出 L
		default:
			a := r.Int63n(L + 1)
			b := r.Int63n(L + 1)
			if a <= b {
				return b + 1, a + 2 // a >= b
			}
			return a + 1, b
		}
	}
	a := r.Int63n(L)
	b := a + 1 + r.Int63n(L-a)
	return a, b
}

func sameErr(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return errors.Is(got, want) || got.Error() == want.Error()
}

// 与朴素模拟对照 2000 组随机序列，日志打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq)*7919 + 17))
		p := randParams(r)
		st, err := New(p)
		if err != nil {
			t.Fatalf("seq %d: New(%+v) = %v", seq, p, err)
		}
		sim := newNaive(p)

		var log strings.Builder
		fmt.Fprintf(&log, "seq=%d 参数: %+v\n", seq, p)
		mismatch := false

		check := func(op string, gotErr, wantErr error) {
			fmt.Fprintf(&log, "  %s => err=%v (朴素: %v)\n", op, gotErr, wantErr)
			if !sameErr(gotErr, wantErr) {
				mismatch = true
				fmt.Fprintf(&log, "  判定: 错误不一致 got=%v want=%v\n", gotErr, wantErr)
			}
		}

		reported := [][2]int64{}
		ops := 5 + r.Intn(35)
		for i := 0; i < ops; i++ {
			switch r.Intn(10) {
			case 0, 1:
				fee := 1 + r.Int63n(1_000_000_000_000)
				if r.Intn(10) == 0 {
					fee = r.Int63n(2) // 偶发非法 fee=0
				}
				check(fmt.Sprintf("NewMonth(%d)", fee), st.NewMonth(fee), sim.newMonth(fee))
			case 2, 3, 4, 5:
				a, b := randInterval(r, p.L)
				check(fmt.Sprintf("Report(%d,%d)", a, b), st.Report(a, b), sim.report(a, b))
				if 0 <= a && a < b && b <= p.L {
					reported = append(reported, [2]int64{a, b})
				}
			case 6, 7:
				a, b := randInterval(r, p.L)
				check(fmt.Sprintf("AddExclude(%d,%d)", a, b), st.AddExclude(a, b), sim.addExclude(a, b))
			case 8:
				var a, b int64
				if len(reported) > 0 && r.Intn(2) == 0 {
					iv := reported[r.Intn(len(reported))]
					a, b = iv[0], iv[1]
				} else {
					a, b = randInterval(r, p.L)
				}
				check(fmt.Sprintf("Revoke(%d,%d)", a, b), st.Revoke(a, b), sim.revoke(a, b))
			case 9:
				gotRes, gotErr := st.Close()
				wantRes, wantErr := sim.close()
				fmt.Fprintf(&log, "  Close() => %+v err=%v (朴素: %+v err=%v)\n", gotRes, gotErr, wantRes, wantErr)
				if !sameErr(gotErr, wantErr) || gotRes != wantRes {
					mismatch = true
					fmt.Fprintf(&log, "  判定: 结算不一致 got=%+v/%v want=%+v/%v\n", gotRes, gotErr, wantRes, wantErr)
				}
				snap := st.State()
				if snap.S != sim.s {
					mismatch = true
					fmt.Fprintf(&log, "  判定: s 不一致 got=%d want=%d\n", snap.S, sim.s)
				}
				for y, paid := range sim.yearPaid {
					if snap.YearPaid[y] != paid {
						mismatch = true
						fmt.Fprintf(&log, "  判定: yearPaid[%d] 不一致 got=%d want=%d\n", y, snap.YearPaid[y], paid)
					}
				}
			}
		}
		fmt.Fprintf(&log, "  判定依据: 逐步对比错误、D/A/base/pct/credit 与 s、yearPaid，朴素模拟按分钟标记数组计算\n")
		if mismatch {
			t.Fatalf("随机序列 %d 与朴素模拟不一致:\n%s", seq, log.String())
		}
		t.Log(log.String())
	}
}
