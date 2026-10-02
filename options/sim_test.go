package options

// 本文件是与引擎独立编写的朴素模拟：所有金额都用 big.Int 计算，
// 逐步直译规格规则，用于随机序列的对照验证。

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// naive 是按规格逐步写成的朴素模拟（大整数）。
type naive struct {
	kind   Kind
	k      int64
	mult   int64
	thresh int64

	expired bool
	seqNext int64
	totalN  int64

	longs  []batch
	shorts []batch

	longCnt   map[string]int64
	margins   map[string]*big.Int
	abstained map[string]bool
	accounts  map[string]bool
}

func newNaive(kind Kind, k, mult, thresh int64) *naive {
	return &naive{
		kind:      kind,
		k:         k,
		mult:      mult,
		thresh:    thresh,
		seqNext:   1,
		longCnt:   make(map[string]int64),
		margins:   make(map[string]*big.Int),
		abstained: make(map[string]bool),
		accounts:  make(map[string]bool),
	}
}

func (n *naive) trade(buyer, seller string, qty int64) (int64, error) {
	if buyer == "" || seller == "" || qty < 1 || qty > MaxTradeN ||
		n.totalN+qty > MaxTotalN {
		return 0, ErrInvalidParam
	}
	if n.expired {
		return 0, ErrExpired
	}
	if buyer == seller {
		return 0, ErrSelfTrade
	}
	seq := n.seqNext
	n.seqNext++
	n.totalN += qty
	n.longs = append(n.longs, batch{acct: buyer, n: qty, seq: seq})
	n.shorts = append(n.shorts, batch{acct: seller, n: qty, seq: seq})
	n.longCnt[buyer]++
	n.accounts[buyer] = true
	n.accounts[seller] = true
	return seq, nil
}

func (n *naive) margin(acct string, g int64) error {
	cur := n.margins[acct]
	if cur == nil {
		cur = new(big.Int)
	}
	if acct == "" || g < 1 || g > MaxMarginG ||
		new(big.Int).Add(cur, big.NewInt(g)).Cmp(big.NewInt(MaxMarginSum)) > 0 {
		return ErrInvalidParam
	}
	if n.expired {
		return ErrExpired
	}
	n.margins[acct] = new(big.Int).Add(cur, big.NewInt(g))
	n.accounts[acct] = true
	return nil
}

func (n *naive) abstain(acct string) error {
	if acct == "" {
		return ErrInvalidParam
	}
	if n.expired {
		return ErrExpired
	}
	if n.longCnt[acct] == 0 {
		return ErrNoLong
	}
	n.abstained[acct] = true
	n.accounts[acct] = true
	return nil
}

// naiveSettlement 为朴素模拟的结算结果，金额保留大整数以便不变量校验。
type naiveSettlement struct {
	v           int64
	q           int64
	exercises   []Exercise
	assignments []Assignment
	defaults    []Default
	netCash     []CashEntry

	recv map[string]*big.Int
	pay  map[string]*big.Int
	loss map[string]*big.Int
	gap  map[string]*big.Int
}

func (n *naive) settle(s int64) (*naiveSettlement, error) {
	if s < 1 || s > MaxSettleS {
		return nil, ErrInvalidParam
	}
	if n.expired {
		return nil, ErrExpired
	}
	n.expired = true

	var v int64
	if n.kind == Call {
		v = max(s-n.k, 0)
	} else {
		v = max(n.k-s, 0)
	}
	res := &naiveSettlement{
		v:    v,
		recv: make(map[string]*big.Int),
		pay:  make(map[string]*big.Int),
		loss: make(map[string]*big.Int),
		gap:  make(map[string]*big.Int),
	}

	// 行权：v >= T 时未放弃账户的全部多头批次自动行权。
	exerciseQty := make(map[string]int64)
	if v >= n.thresh {
		for _, b := range n.longs {
			if !n.abstained[b.acct] {
				exerciseQty[b.acct] += b.n
			}
		}
	}
	exercising := sortedKeys(exerciseQty)
	for _, a := range exercising {
		res.exercises = append(res.exercises, Exercise{Account: a, Qty: exerciseQty[a]})
		res.q += exerciseQty[a]
	}

	// 指派：空头批次按 seq 升序依次分配 Q。
	owe := make(map[string]*big.Int)
	remaining := res.q
	for _, b := range n.shorts {
		q := min(b.n, remaining)
		remaining -= q
		res.assignments = append(res.assignments, Assignment{Seq: b.seq, Account: b.acct, Qty: q})
		if q > 0 {
			add := new(big.Int).Mul(big.NewInt(v*q), big.NewInt(n.mult))
			if owe[b.acct] == nil {
				owe[b.acct] = new(big.Int)
			}
			owe[b.acct].Add(owe[b.acct], add)
		}
	}

	// 应收与实付、缺口。
	totalRecv := new(big.Int)
	for _, a := range exercising {
		r := new(big.Int).Mul(big.NewInt(v*exerciseQty[a]), big.NewInt(n.mult))
		res.recv[a] = r
		totalRecv.Add(totalRecv, r)
	}
	totalGap := new(big.Int)
	for _, a := range sortedKeys(owe) {
		m := n.margins[a]
		if m == nil {
			m = new(big.Int)
		}
		p := new(big.Int).Set(m)
		if p.Cmp(owe[a]) > 0 {
			p = new(big.Int).Set(owe[a])
		}
		res.pay[a] = p
		d := new(big.Int).Sub(owe[a], p)
		res.gap[a] = d
		if d.Sign() > 0 {
			res.defaults = append(res.defaults, Default{Account: a, Amount: d.Int64()})
			totalGap.Add(totalGap, d)
		}
	}

	// 损失分摊：floor(Δ×recv_a/ΣT)，余量按账户字节序各加 1。
	if totalGap.Sign() > 0 {
		distributed := new(big.Int)
		for _, a := range exercising {
			prod := new(big.Int).Mul(totalGap, res.recv[a])
			l := new(big.Int).Quo(prod, totalRecv)
			res.loss[a] = l
			distributed.Add(distributed, l)
		}
		rem := new(big.Int).Sub(totalGap, distributed)
		for i := 0; rem.Sign() > 0; i = (i + 1) % len(exercising) {
			res.loss[exercising[i]].Add(res.loss[exercising[i]], big.NewInt(1))
			rem.Sub(rem, big.NewInt(1))
		}
	}

	// 净现金表：全部出现过的账户。
	for _, a := range sortedKeys(n.accounts) {
		net := new(big.Int)
		if r, ok := res.recv[a]; ok {
			net.Add(net, r)
		}
		if l, ok := res.loss[a]; ok {
			net.Sub(net, l)
		}
		if p, ok := res.pay[a]; ok {
			net.Sub(net, p)
		}
		res.netCash = append(res.netCash, CashEntry{Account: a, Net: net.Int64()})
	}
	return res, nil
}

// randOp 为一次随机操作。
type randOp struct {
	kind string // "trade" / "margin" / "abstain" / "settle"
	a, b string
	n    int64
}

func (o randOp) String() string {
	switch o.kind {
	case "trade":
		return fmt.Sprintf("Trade(%s,%s,%d)", o.a, o.b, o.n)
	case "margin":
		return fmt.Sprintf("Margin(%s,%d)", o.a, o.n)
	case "abstain":
		return fmt.Sprintf("Abstain(%s)", o.a)
	default:
		return fmt.Sprintf("Settle(%d)", o.n)
	}
}

// randCase 为一组随机参数与操作序列。
type randCase struct {
	kind         Kind
	k, mult, thr int64
	ops          []randOp
}

var randAccounts = []string{"A", "B", "C", "X", "Y", "Z", "AB", "A0", "a", "b"}

func genCase(rng *rand.Rand) randCase {
	// 两种画像：小额（容易触发行权与分摊）与大额（容易溢出 64 位乘积）。
	big := rng.Float64() < 0.3
	pick := func(lo, hiSmall, hiBig int64) int64 {
		if big {
			return lo + rng.Int63n(hiBig-lo+1)
		}
		return lo + rng.Int63n(hiSmall-lo+1)
	}
	c := randCase{
		kind: Kind(rng.Intn(2)),
		k:    pick(1, 100, MaxStrike),
		mult: pick(1, 5, MaxMult),
		thr:  pick(1, 10, MaxThreshold),
	}
	acct := func() string { return randAccounts[rng.Intn(len(randAccounts))] }
	numOps := 5 + rng.Intn(36)
	for i := 0; i < numOps; i++ {
		switch r := rng.Float64(); {
		case r < 0.42:
			op := randOp{kind: "trade", a: acct(), b: acct()}
			if rng.Float64() < 0.08 {
				op.b = op.a // 自成交
			}
			switch f := rng.Float64(); {
			case f < 0.05:
				op.n = 0 // 非法
			case f < 0.08:
				op.n = MaxTradeN + 1 // 非法
			default:
				op.n = pick(1, 20, MaxTradeN)
			}
			c.ops = append(c.ops, op)
		case r < 0.67:
			op := randOp{kind: "margin", a: acct()}
			if rng.Float64() < 0.03 {
				op.a = ""
			}
			switch f := rng.Float64(); {
			case f < 0.04:
				op.n = 0 // 非法
			case f < 0.06:
				op.n = MaxMarginG + 1 // 非法
			default:
				op.n = pick(1, 200, MaxMarginG)
			}
			c.ops = append(c.ops, op)
		case r < 0.85:
			op := randOp{kind: "abstain", a: acct()}
			if rng.Float64() < 0.03 {
				op.a = ""
			}
			c.ops = append(c.ops, op)
		default:
			op := randOp{kind: "settle"}
			switch f := rng.Float64(); {
			case f < 0.04:
				op.n = 0 // 非法
			case f < 0.06:
				op.n = MaxSettleS + 1 // 非法
			case big:
				op.n = pick(1, MaxSettleS, MaxSettleS)
			default:
				// 围绕行权价取价，兼顾价内、恰等于门槛与价外。
				base := c.k - 2*c.thr - 2
				if base < 1 {
					base = 1
				}
				op.n = base + rng.Int63n(4*c.thr+8)
				if op.n > MaxSettleS {
					op.n = MaxSettleS
				}
			}
			c.ops = append(c.ops, op)
		}
	}
	return c
}

func errCat(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "参数非法"
	case errors.Is(err, ErrExpired):
		return "已到期"
	case errors.Is(err, ErrSelfTrade):
		return "自成交"
	case errors.Is(err, ErrNoLong):
		return "无多头持仓"
	default:
		return fmt.Sprintf("未知错误 %v", err)
	}
}

// runOps 把操作序列依次施加到引擎与朴素模拟上，逐步比对错误类别与批次序号。
// 返回双方成功的首次 Settle 结果（若有）。
func runOps(t *testing.T, c randCase) (*Settlement, *naiveSettlement) {
	t.Helper()
	e, err := NewEngine(c.kind, c.k, c.mult, c.thr)
	if err != nil {
		t.Fatalf("NewEngine 失败: %v", err)
	}
	n := newNaive(c.kind, c.k, c.mult, c.thr)

	var got *Settlement
	var want *naiveSettlement
	for i, op := range c.ops {
		switch op.kind {
		case "trade":
			seqE, errE := e.Trade(op.a, op.b, op.n)
			seqN, errN := n.trade(op.a, op.b, op.n)
			if errCat(errE) != errCat(errN) {
				t.Fatalf("第 %d 步 %s: 引擎=%s 模拟=%s", i, op, errCat(errE), errCat(errN))
			}
			if errE == nil && seqE != seqN {
				t.Fatalf("第 %d 步 %s: 引擎 seq=%d 模拟 seq=%d", i, op, seqE, seqN)
			}
		case "margin":
			if errE, errN := e.Margin(op.a, op.n), n.margin(op.a, op.n); errCat(errE) != errCat(errN) {
				t.Fatalf("第 %d 步 %s: 引擎=%s 模拟=%s", i, op, errCat(errE), errCat(errN))
			}
		case "abstain":
			if errE, errN := e.Abstain(op.a), n.abstain(op.a); errCat(errE) != errCat(errN) {
				t.Fatalf("第 %d 步 %s: 引擎=%s 模拟=%s", i, op, errCat(errE), errCat(errN))
			}
		case "settle":
			stE, errE := e.Settle(op.n)
			stN, errN := n.settle(op.n)
			if errCat(errE) != errCat(errN) {
				t.Fatalf("第 %d 步 %s: 引擎=%s 模拟=%s", i, op, errCat(errE), errCat(errN))
			}
			if errE == nil && got == nil {
				got, want = stE, stN
			}
		}
	}
	return got, want
}

// checkSettlement 对照引擎与朴素模拟的结算结果，并校验规格要求的不变量。
func checkSettlement(t *testing.T, n *naive, got *Settlement, want *naiveSettlement) {
	t.Helper()
	if got.Intrinsic != want.v || got.TotalExercised != want.q {
		t.Fatalf("引擎 v=%d Q=%d, 模拟 v=%d Q=%d", got.Intrinsic, got.TotalExercised, want.v, want.q)
	}
	if !reflect.DeepEqual(got.Exercises, want.exercises) {
		t.Fatalf("行权清单不一致:\n引擎 %+v\n模拟 %+v", got.Exercises, want.exercises)
	}
	if !reflect.DeepEqual(got.Assignments, want.assignments) {
		t.Fatalf("指派清单不一致:\n引擎 %+v\n模拟 %+v", got.Assignments, want.assignments)
	}
	if !reflect.DeepEqual(got.Defaults, want.defaults) {
		t.Fatalf("违约清单不一致:\n引擎 %+v\n模拟 %+v", got.Defaults, want.defaults)
	}
	if !reflect.DeepEqual(got.NetCash, want.netCash) {
		t.Fatalf("净现金表不一致:\n引擎 %+v\n模拟 %+v", got.NetCash, want.netCash)
	}

	// 不变量 1：各清单按约定排序。
	if !sort.SliceIsSorted(got.Exercises, func(i, j int) bool {
		return got.Exercises[i].Account < got.Exercises[j].Account
	}) {
		t.Fatalf("行权清单未按账户字节序升序: %+v", got.Exercises)
	}
	if !sort.SliceIsSorted(got.Defaults, func(i, j int) bool {
		return got.Defaults[i].Account < got.Defaults[j].Account
	}) {
		t.Fatalf("违约清单未按账户字节序升序: %+v", got.Defaults)
	}
	if !sort.SliceIsSorted(got.NetCash, func(i, j int) bool {
		return got.NetCash[i].Account < got.NetCash[j].Account
	}) {
		t.Fatalf("净现金表未按账户字节序升序: %+v", got.NetCash)
	}

	// 不变量 2：被指派总量等于总行权量 Q；指派只落在 seq 升序的一个
	// 前缀上，除最后一个被触及的批次外之前的批次全部被分满。
	var assigned int64
	seenPartial := false
	for i, a := range got.Assignments {
		if a.Seq != int64(i+1) {
			t.Fatalf("指派清单 seq 不连续: %+v", got.Assignments)
		}
		if a.Qty < 0 || a.Qty > n.shorts[i].n {
			t.Fatalf("批次 %d 分得 %d 超出其数量 %d", a.Seq, a.Qty, n.shorts[i].n)
		}
		if seenPartial && a.Qty != 0 {
			t.Fatalf("部分指派之后仍有非零指派: %+v", got.Assignments)
		}
		if a.Qty < n.shorts[i].n {
			seenPartial = true
		}
		assigned += a.Qty
	}
	if assigned != got.TotalExercised {
		t.Fatalf("被指派总量 %d != 总行权量 %d", assigned, got.TotalExercised)
	}

	// 不变量 3：全部账户净额之和恒为 0。
	sum := new(big.Int)
	for _, c := range got.NetCash {
		sum.Add(sum, big.NewInt(c.Net))
	}
	if sum.Sign() != 0 {
		t.Fatalf("净额合计 %s != 0", sum)
	}

	// 不变量 4：损失之和恒等于 Δ，每个 loss_a 不超过 recv_a，
	// pay_a 不超过该账户保证金。
	totalLoss := new(big.Int)
	for _, l := range want.loss {
		totalLoss.Add(totalLoss, l)
	}
	totalGap := new(big.Int)
	for _, d := range want.gap {
		totalGap.Add(totalGap, d)
	}
	if totalLoss.Cmp(totalGap) != 0 {
		t.Fatalf("损失合计 %s != 缺口 %s", totalLoss, totalGap)
	}
	for a, l := range want.loss {
		if l.Cmp(want.recv[a]) > 0 {
			t.Fatalf("loss_%s = %s > recv %s", a, l, want.recv[a])
		}
	}
	for a, p := range want.pay {
		m := n.margins[a]
		if m == nil {
			m = new(big.Int)
		}
		if p.Cmp(m) > 0 {
			t.Fatalf("pay_%s = %s > 保证金 %s", a, p, m)
		}
	}
}

// TestRandomAgainstNaive 用 2000 组随机登记与结算序列对照朴素模拟，
// 并对每组重放一次以验证相同调用序列得到完全相同的结果。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	settled := 0
	for i := 0; i < 2000; i++ {
		c := genCase(rng)
		kindName := "Call"
		if c.kind == Put {
			kindName = "Put"
		}
		ops := make([]string, len(c.ops))
		for j, op := range c.ops {
			ops[j] = op.String()
		}
		t.Logf("用例 %d 输入: 类型=%s K=%d Mu=%d T=%d 操作=[%s]",
			i, kindName, c.k, c.mult, c.thr, strings.Join(ops, "; "))

		got, want := runOps(t, c)
		if got != nil {
			settled++
			checkSettlement(t, newNaiveReplay(c), got, want)
			t.Logf("用例 %d 输出: v=%d Q=%d 行权=%+v 指派=%+v 违约=%+v 净现金=%+v",
				i, got.Intrinsic, got.TotalExercised,
				got.Exercises, got.Assignments, got.Defaults, got.NetCash)
			t.Logf("用例 %d 判定依据: 引擎与朴素模拟四张清单完全一致；指派总量=Q、前缀性质、净额合计为 0、损失合计=Δ 均校验通过", i)
		} else {
			t.Logf("用例 %d 输出: 无成功 Settle；逐步错误类别与朴素模拟一致", i)
		}

		// 重放：相同调用序列须得到完全相同的结果。
		reGot, _ := runOps(t, c)
		if (got == nil) != (reGot == nil) {
			t.Fatalf("用例 %d 重放结果不一致", i)
		}
		if got != nil && !reflect.DeepEqual(got, reGot) {
			t.Fatalf("用例 %d 重放结算结果不一致:\n首次 %+v\n重放 %+v", i, got, reGot)
		}
	}
	t.Logf("共 2000 组随机序列，其中 %d 组发生了成功结算", settled)
}

// newNaiveReplay 重放操作序列，返回结算后（含保证金等）的最终模拟状态。
func newNaiveReplay(c randCase) *naive {
	n := newNaive(c.kind, c.k, c.mult, c.thr)
	for _, op := range c.ops {
		switch op.kind {
		case "trade":
			_, _ = n.trade(op.a, op.b, op.n)
		case "margin":
			_ = n.margin(op.a, op.n)
		case "abstain":
			_ = n.abstain(op.a)
		case "settle":
			_, _ = n.settle(op.n)
		}
	}
	return n
}
