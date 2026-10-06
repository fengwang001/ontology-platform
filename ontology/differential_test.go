package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// diffWorld 持有正式引擎与独立朴素模型，并向二者发送完全相同的操作流。
type diffWorld struct {
	t       *testing.T
	real    *Engine
	naive   *NaiveEngine
	rng     *rand.Rand
	starts  []time.Time // 全部候选间隔起点（months 个自然月）
	months  []MonthKey
	sealed  map[string]int // consumer -> 已封账月数
	nCons   int
	logStep int
}

func newDiffWorld(t *testing.T, seed int64, nMonths int, nCons int) *diffWorld {
	t.Helper()
	real, err := NewEngine(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	naive, err := NewNaiveEngine(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w := &diffWorld{
		t: t, real: real, naive: naive, rng: rand.New(rand.NewSource(seed)),
		sealed: map[string]int{}, nCons: nCons,
	}
	base := month(t, "2025-01")
	for k := 0; k < nMonths; k++ {
		m := base.Add(int64(k))
		w.months = append(w.months, m)
		w.starts = append(w.starts, real.g.IntervalStarts(m)...)
	}
	for i := 0; i < nCons; i++ {
		id := fmt.Sprintf("p%d", i)
		p := w.randParams()
		pr := w.randPrices()
		if err := real.RegisterConsumer(id, p, pr); err != nil {
			t.Fatal(err)
		}
		if err := naive.RegisterConsumer(id, p, pr); err != nil {
			t.Fatal(err)
		}
		w.sealed[id] = 0
		t.Logf("INPUT RegisterConsumer id=%s params=%+v prices=%+v -> OUTPUT ok", id, p, pr)
	}
	return w
}

func (w *diffWorld) randParams() ContractParams {
	return ContractParams{
		ContractExportWatts:  int64(w.rng.Intn(6)) * 1000, // 0..5 kW
		MonthlyCreditableKWh: int64(w.rng.Intn(120)),
		CreditValidMonths:    int64(w.rng.Intn(4)),
	}
}

func (w *diffWorld) randPrices() Prices {
	return Prices{ImportPrice: int64(w.rng.Intn(8)), ExportPrice: int64(w.rng.Intn(5))}
}

func (w *diffWorld) cid() string { return fmt.Sprintf("p%d", w.rng.Intn(w.nCons)) }

func kindOf(err error) ErrorKind {
	if err == nil {
		return 0
	}
	var se *SettError
	if errors.As(err, &se) {
		return se.Kind
	}
	return -1
}

// apply 发送同一操作到两个模型，校验错误类别一致，必要时校验结果一致。
func (w *diffWorld) apply(desc string, fn func(m modelAPI) (any, error)) {
	w.logStep++
	ra, rerr := fn(w.real)
	na, nerr := fn(w.naive)
	if kindOf(rerr) != kindOf(nerr) {
		w.t.Fatalf("STEP %d %s: error kind mismatch real=%v naive=%v",
			w.logStep, desc, rerr, nerr)
	}
	w.t.Logf("STEP %d INPUT %s -> OUTPUT real=%v err=%v | naive=%v err=%v | 判定=%s",
		w.logStep, desc, fmtResult(ra), rerr, fmtResult(na), nerr, verdict(rerr))
	if rerr != nil {
		return
	}
	if rr, ok := ra.(MonthlyResult); ok {
		nr := na.(MonthlyResult)
		if rr != nr {
			w.t.Fatalf("STEP %d %s: MonthlyResult mismatch\nreal =%+v\nnaive=%+v", w.logStep, desc, rr, nr)
		}
	}
	if rb, ok := ra.(int64); ok {
		if nb := na.(int64); rb != nb {
			w.t.Fatalf("STEP %d %s: balance mismatch real=%d naive=%d", w.logStep, desc, rb, nb)
		}
	}
}

type modelAPI interface {
	SetParams(id string, from MonthKey, p ContractParams) error
	SetPrices(id string, from MonthKey, pr Prices) error
	PutReading(id string, start time.Time, r Readings) error
	SealMonth(id string, m MonthKey) error
	MonthResult(id string, m MonthKey) (MonthlyResult, error)
	CreditBalance(id string) (int64, error)
}

func verdict(err error) string {
	if err == nil {
		return "接受"
	}
	return "拒绝(" + err.Error() + ")"
}

func fmtResult(v any) string {
	if v == nil {
		return "ok"
	}
	return fmt.Sprintf("%+v", v)
}

// TestRandomDifferential 用大量随机操作序列对照朴素模型。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	const seeds = 40
	for seed := int64(1); seed <= seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			w := newDiffWorld(t, seed, 3, 3)
			runOneSeed(t, w, seed)
		})
	}
}

func runOneSeed(t *testing.T, w *diffWorld, seed int64) {
	const steps = 1500
	for i := 0; i < steps; i++ {
		id := w.cid()
		nSealed := w.sealed[id]
		nextMonth := w.months[0]
		if nSealed < len(w.months) {
			nextMonth = w.months[nSealed]
		}
		switch w.rng.Intn(10) {
		case 0, 1, 2, 3, 4, 5: // 读数：大多数合法，少量制造对齐/负数非法
			start := w.starts[w.rng.Intn(len(w.starts))]
			if w.rng.Intn(15) == 0 {
				start = start.Add(time.Duration(w.rng.Intn(50)+1) * time.Minute)
			}
			r := Readings{ImportKWh: int64(w.rng.Intn(12)), ExportKWh: int64(w.rng.Intn(12))}
			if w.rng.Intn(20) == 0 {
				r.ImportKWh = -1
			}
			desc := fmt.Sprintf("PutReading id=%s start=%s imp=%d exp=%d", id, start.UTC().Format(time.RFC3339), r.ImportKWh, r.ExportKWh)
			w.apply(desc, func(m modelAPI) (any, error) { return nil, m.PutReading(id, start, r) })
		case 6: // 封账下一个月
			if nSealed < len(w.months) {
				m := w.months[nSealed]
				desc := fmt.Sprintf("SealMonth id=%s month=%s", id, m)
				rerr := w.real.SealMonth(id, m)
				nerr := w.naive.SealMonth(id, m)
				if kindOf(rerr) != kindOf(nerr) {
					t.Fatalf("seal mismatch: real=%v naive=%v", rerr, nerr)
				}
				t.Logf("INPUT %s -> OUTPUT real=%v naive=%v | %s", desc, rerr, nerr, verdict(rerr))
				if rerr == nil {
					w.sealed[id]++
				}
			}
		case 7: // 尝试跳月封账（可能被顺序错误拒绝）
			if nSealed+1 < len(w.months) {
				mo := w.months[nSealed+1]
				desc := fmt.Sprintf("SealMonth(skip) id=%s month=%s", id, mo)
				w.apply(desc, func(api modelAPI) (any, error) { return nil, api.SealMonth(id, mo) })
			}
		case 8: // 参数更改：从未封账的某月起
			from := nextMonth
			if w.rng.Intn(3) == 0 && nSealed > 0 {
				from = w.months[nSealed-1] // 故意指向已封账月
			}
			p := w.randParams()
			desc := fmt.Sprintf("SetParams id=%s from=%s p=%+v", id, from, p)
			w.apply(desc, func(m modelAPI) (any, error) { return nil, m.SetParams(id, from, p) })
		case 9: // 单价更改 / 查询
			if w.rng.Intn(2) == 0 {
				from := nextMonth
				if w.rng.Intn(3) == 0 && nSealed > 0 {
					from = w.months[nSealed-1]
				}
				pr := w.randPrices()
				if w.rng.Intn(15) == 0 {
					pr.ImportPrice = -2
				}
				desc := fmt.Sprintf("SetPrices id=%s from=%s pr=%+v", id, from, pr)
				w.apply(desc, func(m modelAPI) (any, error) { return nil, m.SetPrices(id, from, pr) })
			} else {
				mi := w.rng.Intn(len(w.months))
				mo := w.months[mi]
				desc := fmt.Sprintf("MonthResult id=%s month=%s", id, mo)
				w.apply(desc, func(api modelAPI) (any, error) { return api.MonthResult(id, mo) })
			}
		}
		// 周期性查询全部月结果与余额，校验一致性。
		if i%97 == 0 {
			for _, mo := range w.months {
				desc := fmt.Sprintf("MonthResult(check) id=%s month=%s", id, mo)
				w.apply(desc, func(api modelAPI) (any, error) { return api.MonthResult(id, mo) })
			}
			desc := fmt.Sprintf("CreditBalance id=%s", id)
			w.apply(desc, func(api modelAPI) (any, error) { return api.CreditBalance(id) })
		}
		// 若封账成功，推进该产消者的已封账计数：通过与 naive 对齐的方式判定。
		// （case 6 中直接再次查询成本高；改为尝试后检查 upTo。）
	}
	// 收尾：尽力把所有月补齐并封账，再逐月对照。
	for id, nSealed := range w.sealed {
		for k := nSealed; k < len(w.months); k++ {
			mo := w.months[k]
			for _, st := range w.real.g.IntervalStarts(mo) {
				r := Readings{ImportKWh: int64(w.rng.Intn(8)), ExportKWh: int64(w.rng.Intn(8))}
				_ = w.real.PutReading(id, st, r)
				_ = w.naive.PutReading(id, st, r)
			}
			desc := fmt.Sprintf("SealMonth(final) id=%s month=%s", id, mo)
			var becameSealed bool
			w.apply(desc, func(api modelAPI) (any, error) {
				err := api.SealMonth(id, mo)
				if err == nil {
					becameSealed = true
				}
				return nil, err
			})
			if becameSealed {
				w.sealed[id]++
			}
		}
		for _, mo := range w.months {
			desc := fmt.Sprintf("MonthResult(final) id=%s month=%s", id, mo)
			w.apply(desc, func(api modelAPI) (any, error) { return api.MonthResult(id, mo) })
		}
	}
}
