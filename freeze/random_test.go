package freeze_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/freeze"
)

// codeOf 把 manager 返回的 error 映射为可比较的错误码，-1 表示成功。
func codeOf(err error) int {
	if err == nil {
		return -1
	}
	var fe *freeze.Error
	if errors.As(err, &fe) {
		return int(fe.Code)
	}
	return -2
}

// ---------- 朴素模拟器：按题述规则逐令计算的独立参照实现 ----------

type naiveOrder struct {
	id  string
	a   int64
	exp int64
}

type naiveAcct struct {
	b int64
	q []naiveOrder
}

type naiveSim struct {
	accts map[string]*naiveAcct
	m     int64
}

func newNaiveSim() *naiveSim {
	return &naiveSim{accts: make(map[string]*naiveAcct)}
}

func naiveEffective(b int64, q []naiveOrder) []int64 {
	es := make([]int64, len(q))
	used := int64(0)
	for i, o := range q {
		rem := b - used
		if rem < 0 {
			rem = 0
		}
		e := o.a
		if e > rem {
			e = rem
		}
		es[i] = e
		used += e
	}
	return es
}

func naivePurge(q []naiveOrder, t int64) []naiveOrder {
	out := make([]naiveOrder, 0, len(q))
	for _, o := range q {
		if o.exp == 0 || o.exp > t {
			out = append(out, o)
		}
	}
	return out
}

func validAmt(x int64) bool { return x >= 1 && x <= freeze.MaxAmount }
func validT(t int64) bool   { return t >= 0 && t <= freeze.MaxAmount }

// 各操作返回 (错误码, 判定依据)，错误码 -1 表示成功。

func (s *naiveSim) deposit(acct string, t, x int64) (int, string) {
	b := int64(0)
	if a, ok := s.accts[acct]; ok {
		b = a.b
	}
	if acct == "" || !validT(t) || !validAmt(x) || b+x > freeze.MaxBalance {
		return int(freeze.ErrInvalidParam), "参数非法"
	}
	if t < s.m {
		return int(freeze.ErrTimeRegression), fmt.Sprintf("t=%d < m=%d", t, s.m)
	}
	a, ok := s.accts[acct]
	if !ok {
		a = &naiveAcct{}
		s.accts[acct] = a
	}
	a.q = naivePurge(a.q, t)
	a.b += x
	s.m = t
	return -1, fmt.Sprintf("B: %d -> %d", b, a.b)
}

func (s *naiveSim) getAcct(acct string, t int64) (*naiveAcct, int, string) {
	if acct == "" || !validT(t) {
		return nil, int(freeze.ErrInvalidParam), "参数非法"
	}
	if t < s.m {
		return nil, int(freeze.ErrTimeRegression), fmt.Sprintf("t=%d < m=%d", t, s.m)
	}
	a, ok := s.accts[acct]
	if !ok {
		return nil, int(freeze.ErrAccountNotFound), "账户不存在"
	}
	return a, -1, ""
}

func (s *naiveSim) debit(acct string, t, x int64) (int, string) {
	if !validAmt(x) {
		return int(freeze.ErrInvalidParam), "参数非法"
	}
	a, c, why := s.getAcct(acct, t)
	if c >= 0 {
		return c, why
	}
	q := naivePurge(a.q, t)
	used := int64(0)
	for _, e := range naiveEffective(a.b, q) {
		used += e
	}
	v := a.b - used
	if x > v {
		return int(freeze.ErrInsufficientAvail), fmt.Sprintf("x=%d > V=%d", x, v)
	}
	a.q = q
	a.b -= x
	s.m = t
	return -1, fmt.Sprintf("V=%d, B: %d -> %d", v, a.b+x, a.b)
}

func (s *naiveSim) freezeOrder(acct string, t int64, id string, amt, exp int64) (int, string) {
	if id == "" || !validAmt(amt) || !(exp == 0 || (exp > t && exp <= freeze.MaxAmount)) {
		return int(freeze.ErrInvalidParam), "参数非法"
	}
	a, c, why := s.getAcct(acct, t)
	if c >= 0 {
		return c, why
	}
	q := naivePurge(a.q, t)
	for _, o := range q {
		if o.id == id {
			return int(freeze.ErrDuplicateOrder), "令编号重复"
		}
	}
	a.q = append(q, naiveOrder{id: id, a: amt, exp: exp})
	s.m = t
	return -1, fmt.Sprintf("追加令 %s a=%d exp=%d", id, amt, exp)
}

func (s *naiveSim) unfreeze(acct string, t int64, id string, x int64) (int, string) {
	if id == "" || !validAmt(x) {
		return int(freeze.ErrInvalidParam), "参数非法"
	}
	a, c, why := s.getAcct(acct, t)
	if c >= 0 {
		return c, why
	}
	q := naivePurge(a.q, t)
	idx := -1
	for i, o := range q {
		if o.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return int(freeze.ErrOrderNotFound), "令不存在"
	}
	if x > q[idx].a {
		return int(freeze.ErrUnfreezeExceeds), fmt.Sprintf("x=%d > a=%d", x, q[idx].a)
	}
	q[idx].a -= x
	remain := q[idx].a
	if q[idx].a == 0 {
		q = append(q[:idx], q[idx+1:]...)
	}
	a.q = q
	s.m = t
	return -1, fmt.Sprintf("a: -> %d", remain)
}

func (s *naiveSim) seize(acct string, t int64, id string, x int64) (int, string) {
	if id == "" || !validAmt(x) {
		return int(freeze.ErrInvalidParam), "参数非法"
	}
	a, c, why := s.getAcct(acct, t)
	if c >= 0 {
		return c, why
	}
	q := naivePurge(a.q, t)
	idx := -1
	for i, o := range q {
		if o.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return int(freeze.ErrOrderNotFound), "令不存在"
	}
	e := naiveEffective(a.b, q)[idx]
	if x > e {
		return int(freeze.ErrSeizeExceeds), fmt.Sprintf("x=%d > e=%d", x, e)
	}
	q[idx].a -= x
	if q[idx].a == 0 {
		q = append(q[:idx], q[idx+1:]...)
	}
	a.q = q
	a.b -= x
	s.m = t
	return -1, fmt.Sprintf("e=%d, B: -> %d", e, a.b)
}

func (s *naiveSim) seizeQ(acct string, t, x int64) (int, string) {
	if !validAmt(x) {
		return int(freeze.ErrInvalidParam), "参数非法"
	}
	a, c, why := s.getAcct(acct, t)
	if c >= 0 {
		return c, why
	}
	q := naivePurge(a.q, t)
	es := naiveEffective(a.b, q)
	total := int64(0)
	for _, e := range es {
		total += e
	}
	if x > total {
		return int(freeze.ErrSeizeQExceeds), fmt.Sprintf("x=%d > sum(e)=%d", x, total)
	}
	rem := x
	out := make([]naiveOrder, 0, len(q))
	for i, o := range q {
		d := es[i]
		if d > rem {
			d = rem
		}
		rem -= d
		o.a -= d
		if o.a > 0 {
			out = append(out, o)
		}
	}
	a.q = out
	a.b -= x
	s.m = t
	return -1, fmt.Sprintf("sum(e)=%d, B: -> %d", total, a.b)
}

func (s *naiveSim) query(acct string, t int64) (freeze.AccountView, int, string) {
	a, c, why := s.getAcct(acct, t)
	if c >= 0 {
		return freeze.AccountView{}, c, why
	}
	q := naivePurge(a.q, t)
	es := naiveEffective(a.b, q)
	view := freeze.AccountView{B: a.b, Orders: make([]freeze.OrderView, len(q))}
	used := int64(0)
	for i, o := range q {
		view.Orders[i] = freeze.OrderView{ID: o.id, A: o.a, E: es[i], Exp: o.exp}
		used += es[i]
	}
	view.V = a.b - used
	a.q = q
	s.m = t
	return view, -1, fmt.Sprintf("B=%d V=%d 令数=%d", view.B, view.V, len(q))
}

// ---------- 随机对照测试 ----------

// checkView 校验有效状态的不变量：V 非负、sum(e) <= B、0 <= e_i <= a_i。
func checkView(t *testing.T, seq, op int, view freeze.AccountView) {
	t.Helper()
	sum := int64(0)
	for _, o := range view.Orders {
		if o.E < 0 || o.E > o.A {
			t.Fatalf("seq=%d op=%d 不变量违反：令 %+v 的 e 不在 [0,a] 内", seq, op, o)
		}
		sum += o.E
	}
	if sum > view.B {
		t.Fatalf("seq=%d op=%d 不变量违反：sum(e)=%d > B=%d", seq, op, sum, view.B)
	}
	if view.V != view.B-sum || view.V < 0 {
		t.Fatalf("seq=%d op=%d 不变量违反：V=%d, B=%d, sum(e)=%d", seq, op, view.V, view.B, sum)
	}
}

func randAmount(rng *rand.Rand) int64 {
	switch rng.Intn(10) {
	case 0:
		return 0 // 非法
	case 1:
		return freeze.MaxAmount + 1 // 非法
	case 2:
		return -rng.Int63n(100) - 1 // 非法
	case 3, 4:
		return rng.Int63n(freeze.MaxAmount) + 1 // 大额合法
	default:
		return rng.Int63n(150) + 1 // 小额合法
	}
}

func randExp(rng *rand.Rand, tm int64) int64 {
	switch rng.Intn(10) {
	case 0, 1, 2, 3:
		return 0 // 永不到期
	case 4, 5, 6, 7:
		return tm + 1 + rng.Int63n(10) // 合法到期时刻
	case 8:
		return tm // 非法：不大于登记时刻
	default:
		return freeze.MaxAmount + 1 // 非法：超过上界
	}
}

// TestRandomAgainstNaive 2000 组随机操作序列与朴素模拟对照，
// 日志打印每步的输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	accts := []string{"a0", "a1", "a2"}
	ids := []string{"id0", "id1", "id2", "id3", "id4", "id5"}

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		mg := freeze.NewManager()
		sim := newNaiveSim()
		var deposited, debited, seized int64
		cur := int64(0)
		ops := 20 + rng.Intn(30)

		for op := 0; op < ops; op++ {
			// 时刻：多数非递减，少数尝试倒退。
			if rng.Intn(20) == 0 && cur > 0 {
				cur -= 1 + rng.Int63n(cur+1)
			} else {
				cur += rng.Int63n(3)
			}
			tm := cur
			acct := accts[rng.Intn(len(accts))]
			if rng.Intn(50) == 0 {
				acct = "" // 非法账户编号
			}
			id := ids[rng.Intn(len(ids))]
			kind := rng.Intn(100)

			var gotCode, wantCode int
			var desc, reason string
			switch {
			case kind < 20: // Deposit
				x := randAmount(rng)
				desc = fmt.Sprintf("Deposit(%q,t=%d,x=%d)", acct, tm, x)
				gotCode = codeOf(mg.Deposit(acct, tm, x))
				wantCode, reason = sim.deposit(acct, tm, x)
				if gotCode == -1 {
					deposited += x
				}
			case kind < 35: // Debit
				x := randAmount(rng)
				desc = fmt.Sprintf("Debit(%q,t=%d,x=%d)", acct, tm, x)
				gotCode = codeOf(mg.Debit(acct, tm, x))
				wantCode, reason = sim.debit(acct, tm, x)
				if gotCode == -1 {
					debited += x
				}
			case kind < 55: // Freeze
				a := randAmount(rng)
				exp := randExp(rng, tm)
				desc = fmt.Sprintf("Freeze(%q,t=%d,id=%q,a=%d,exp=%d)", acct, tm, id, a, exp)
				gotCode = codeOf(mg.Freeze(acct, tm, id, a, exp))
				wantCode, reason = sim.freezeOrder(acct, tm, id, a, exp)
			case kind < 67: // Unfreeze
				x := randAmount(rng)
				desc = fmt.Sprintf("Unfreeze(%q,t=%d,id=%q,x=%d)", acct, tm, id, x)
				gotCode = codeOf(mg.Unfreeze(acct, tm, id, x))
				wantCode, reason = sim.unfreeze(acct, tm, id, x)
			case kind < 79: // Seize
				x := randAmount(rng)
				desc = fmt.Sprintf("Seize(%q,t=%d,id=%q,x=%d)", acct, tm, id, x)
				gotCode = codeOf(mg.Seize(acct, tm, id, x))
				wantCode, reason = sim.seize(acct, tm, id, x)
				if gotCode == -1 {
					seized += x
				}
			case kind < 87: // SeizeQ
				x := randAmount(rng)
				desc = fmt.Sprintf("SeizeQ(%q,t=%d,x=%d)", acct, tm, x)
				gotCode = codeOf(mg.SeizeQ(acct, tm, x))
				wantCode, reason = sim.seizeQ(acct, tm, x)
				if gotCode == -1 {
					seized += x
				}
			default: // Query
				desc = fmt.Sprintf("Query(%q,t=%d)", acct, tm)
				gotView, err := mg.Query(acct, tm)
				gotCode = codeOf(err)
				var wantView freeze.AccountView
				wantView, wantCode, reason = sim.query(acct, tm)
				if gotCode == -1 && wantCode == -1 && !reflect.DeepEqual(gotView, wantView) {
					t.Fatalf("seq=%d op=%d %s 查询不一致:\nmanager=%+v\nnaive  =%+v",
						seq, op, desc, gotView, wantView)
				}
			}

			if gotCode != wantCode {
				t.Fatalf("seq=%d op=%d %s 判定不一致: manager=%d naive=%d（%s）",
					seq, op, desc, gotCode, wantCode, reason)
			}
			outcome := "OK"
			if gotCode >= 0 {
				outcome = fmt.Sprintf("REJECT(code=%d)", gotCode)
			}
			t.Logf("seq=%d op=%d %s -> %s [%s]", seq, op, desc, outcome, reason)

			if mg.MaxTime() != sim.m {
				t.Fatalf("seq=%d op=%d %s 后 m 不一致: manager=%d naive=%d",
					seq, op, desc, mg.MaxTime(), sim.m)
			}
			// 每步后在当前 m 上对照账户有效状态并校验不变量。
			if acct != "" {
				mNow := mg.MaxTime()
				gotView, err := mg.Query(acct, mNow)
				wantView, wCode, _ := sim.query(acct, mNow)
				if codeOf(err) != wCode {
					t.Fatalf("seq=%d op=%d 后查询判定不一致: manager=%d naive=%d",
						seq, op, codeOf(err), wCode)
				}
				if wCode == -1 {
					if !reflect.DeepEqual(gotView, wantView) {
						t.Fatalf("seq=%d op=%d 后状态不一致:\nmanager=%+v\nnaive  =%+v",
							seq, op, gotView, wantView)
					}
					checkView(t, seq, op, gotView)
				}
			}
		}

		// 守恒：全部余额之和 + 已扣划 + 已 Debit == 全部存入。
		mNow := mg.MaxTime()
		sumB := int64(0)
		for _, name := range accts {
			view, err := mg.Query(name, mNow)
			if err != nil {
				continue
			}
			sumB += view.B
		}
		if sumB+seized+debited != deposited {
			t.Fatalf("seq=%d 守恒违反: sumB=%d seized=%d debited=%d deposited=%d",
				seq, sumB, seized, debited, deposited)
		}
	}
}

// TestConcurrent 并发调用等价于某个串行顺序：同刻 t=0 下多 goroutine 混合操作，
// 校验无数据竞争、守恒成立、每次查询快照都满足不变量（SeizeQ 为原子步骤）。
func TestConcurrent(t *testing.T) {
	mg := freeze.NewManager()
	const workers = 8
	const opsPerWorker = 300
	var deposited, debited, seized atomic.Int64
	var badView atomic.Int64

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)*104729 + 7))
			acct := fmt.Sprintf("acct-%d", w%3)
			for i := 0; i < opsPerWorker; i++ {
				id := fmt.Sprintf("id-%d", rng.Intn(6))
				switch rng.Intn(8) {
				case 0:
					x := rng.Int63n(100) + 1
					if mg.Deposit(acct, 0, x) == nil {
						deposited.Add(x)
					}
				case 1:
					x := rng.Int63n(50) + 1
					if mg.Debit(acct, 0, x) == nil {
						debited.Add(x)
					}
				case 2:
					_ = mg.Freeze(acct, 0, id, rng.Int63n(200)+1, 0)
				case 3:
					_ = mg.Unfreeze(acct, 0, id, rng.Int63n(50)+1)
				case 4:
					x := rng.Int63n(50) + 1
					if mg.Seize(acct, 0, id, x) == nil {
						seized.Add(x)
					}
				case 5:
					x := rng.Int63n(80) + 1
					if mg.SeizeQ(acct, 0, x) == nil {
						seized.Add(x)
					}
				default:
					view, err := mg.Query(acct, 0)
					if err != nil {
						continue
					}
					sum := int64(0)
					for _, o := range view.Orders {
						if o.E < 0 || o.E > o.A {
							badView.Add(1)
						}
						sum += o.E
					}
					if sum > view.B || view.V != view.B-sum || view.V < 0 {
						badView.Add(1)
					}
				}
			}
		}(w)
	}
	wg.Wait()

	if badView.Load() != 0 {
		t.Fatalf("并发查询出现 %d 次不变量违反", badView.Load())
	}
	sumB := int64(0)
	for _, acct := range []string{"acct-0", "acct-1", "acct-2"} {
		view, err := mg.Query(acct, 0)
		mustOK(t, err)
		sumB += view.B
	}
	if got, want := sumB+seized.Load()+debited.Load(), deposited.Load(); got != want {
		t.Fatalf("并发守恒违反: sumB+seized+debited=%d deposited=%d", got, want)
	}
}
