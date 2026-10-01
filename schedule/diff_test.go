package schedule

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// 操作序列里的一步；脚本可被重放以验证确定性。
type op struct {
	kind   string // create cancel reschedule split
	id     string
	date   string
	target string
	newID  string
	rule   Rule
	term   Termination
	start  string
}

func applyProd(m *Manager, o op) error {
	switch o.kind {
	case "create":
		return m.Create(CreateInput{ID: o.id, Start: o.start, Rule: o.rule, Term: o.term})
	case "cancel":
		return m.Cancel(o.id, o.date)
	case "reschedule":
		return m.Reschedule(o.id, o.date, o.target)
	case "split":
		return m.Split(SplitInput{ID: o.id, Date: o.date, NewID: o.newID, NewRule: o.rule})
	}
	return fmt.Errorf("unknown op %s", o.kind)
}

func applyNaive(mm *naiveModel, o op) ErrCode {
	switch o.kind {
	case "create":
		return mm.create(CreateInput{ID: o.id, Start: o.start, Rule: o.rule, Term: o.term})
	case "cancel":
		return mm.cancel(o.id, o.date)
	case "reschedule":
		return mm.reschedule(o.id, o.date, o.target)
	case "split":
		return mm.split(SplitInput{ID: o.id, Date: o.date, NewID: o.newID, NewRule: o.rule})
	}
	return "UNKNOWN_OP"
}

var pool = []string{
	"2023-06-05", "2023-09-04", "2023-12-04",
	"2024-01-29", "2024-02-26", "2024-03-25", "2024-04-22", "2024-05-27",
	"2024-06-24", "2024-07-22", "2024-08-26", "2024-09-23", "2024-10-28",
	"2024-11-25", "2024-12-30", "2025-01-27", "2025-02-24", "2025-03-31",
	"2025-04-28", "2025-05-26", "2025-06-30", "2025-07-28", "2025-08-25",
	"2025-09-29", "2025-10-27", "2025-11-24", "2025-12-29",
}

func randomRule(r *rand.Rand) Rule {
	nth := 1 + r.Intn(5)
	if r.Intn(5) == 0 {
		nth = -1
	}
	return Rule{
		IntervalMonths: 1 + r.Intn(6),
		Nth:            nth,
		Weekday:        1 + r.Intn(7),
	}
}

// genScript 生成一段随机但合法/非法混杂的操作脚本。
func genScript(r *rand.Rand, n int) []op {
	var ops []op
	seriesIDs := []string{}
	// 每个系列维护其候选实例原日期（由生产逻辑枚举时再查；这里记规则起点，
	// 实例日期从候选池中挑“可能”的日期，非法操作也允许，用于比对拒绝结果）。
	type info struct {
		start string
		rule  Rule
		term  Termination
	}
	known := map[string]info{}
	for i := 0; i < n; i++ {
		switch {
		case len(seriesIDs) == 0 || r.Intn(3) == 0:
			id := fmt.Sprintf("s%d", len(seriesIDs))
			start := pool[r.Intn(len(pool)-8)]
			rule := randomRule(r)
			term := Termination{Count: 1 + r.Intn(10)}
			if r.Intn(2) == 0 {
				term = Termination{Until: pool[len(pool)-1-r.Intn(6)]}
			}
			in := info{start: start, rule: rule, term: term}
			known[id] = in
			seriesIDs = append(seriesIDs, id)
			ops = append(ops, op{kind: "create", id: id, start: start, rule: rule, term: term})
		default:
			id := seriesIDs[r.Intn(len(seriesIDs))]
			inf := known[id]
			_ = inf
			date := pool[r.Intn(len(pool))]
			switch r.Intn(4) {
			case 0:
				ops = append(ops, op{kind: "cancel", id: id, date: date})
			case 1:
				tgt := pool[r.Intn(len(pool))]
				ops = append(ops, op{kind: "reschedule", id: id, date: date, target: tgt})
			case 2:
				newID := fmt.Sprintf("split%d", r.Intn(100000))
				ops = append(ops, op{kind: "split", id: id, date: date, newID: newID, rule: randomRule(r)})
			default:
				// 少量重复创建，用于比对 duplicate 拒绝。
				ops = append(ops, op{kind: "create", id: id, start: inf.start, rule: inf.rule, term: inf.term})
			}
		}
	}
	return ops
}

// 差分：同一脚本喂给生产实现与朴素实现，逐步比对拒绝码与展开结果。
func TestDifferentialAgainstNaive(t *testing.T) {
	windows := [][2]string{
		{"2023-01-01", "2024-01-01"},
		{"2024-03-01", "2024-09-01"},
		{"2025-01-01", "2025-07-01"},
		{"2023-01-01", "2026-01-01"},
		{"2024-01-01", "2034-01-08"}, // 恰好 3660 天上限
	}
	for seed := int64(1); seed <= 200; seed++ {
		r := rand.New(rand.NewSource(seed))
		ops := genScript(r, 40)
		prod := NewManager(nil)
		naive := newNaiveModel()
		for step, o := range ops {
			perr := applyProd(prod, o)
			nerr := applyNaive(naive, o)
			if codeOf(perr) != nerr {
				t.Fatalf("seed=%d step=%d op=%+v prod=%v naive=%s", seed, step, o, perr, nerr)
			}
			if r.Intn(3) == 0 {
				w := windows[r.Intn(len(windows))]
				pins, _ := prod.Expand(w[0], w[1])
				nins, _ := naive.expand(w[0], w[1])
				if !reflect.DeepEqual(pins, nins) {
					t.Fatalf("seed=%d step=%d window=%v expand mismatch\nprod =%v\nnaive=%v",
						seed, step, w, dumpInstances(pins), dumpInstances(nins))
				}
			}
		}
		for _, w := range windows {
			pins, _ := prod.Expand(w[0], w[1])
			nins, _ := naive.expand(w[0], w[1])
			if !reflect.DeepEqual(pins, nins) {
				t.Fatalf("seed=%d final window=%v mismatch\nprod =%v\nnaive=%v",
					seed, w, dumpInstances(pins), dumpInstances(nins))
			}
		}
	}
}

// 相同操作序列重放两次，展开结果完全一致。
func TestReplayDeterminism(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	ops := genScript(r, 120)
	run := func() []Instance {
		m := NewManager(nil)
		for _, o := range ops {
			_ = applyProd(m, o)
		}
		ins, err := m.Expand("2023-01-01", "2026-01-01")
		if err != nil {
			t.Fatal(err)
		}
		return ins
	}
	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch:\nfirst =%v\nsecond=%v", first, second)
	}
}

// 并发调用不发生数据竞争，且最终状态等价于某个串行顺序：
// 每个 goroutine 使用独立 id 区间做 create，结束后实例总数确定。
func TestConcurrent(t *testing.T) {
	m := NewManager(nil)
	const g = 12
	var wg sync.WaitGroup
	for gi := 0; gi < g; gi++ {
		wg.Add(1)
		go func(gi int) {
			defer wg.Done()
			id := fmt.Sprintf("c%02d", gi)
			err := m.Create(CreateInput{
				ID: id, Start: "2025-01-06",
				Rule: Rule{IntervalMonths: 1, Nth: 1, Weekday: 1},
				Term: Termination{Count: 3},
			})
			if err != nil {
				t.Errorf("create %s: %v", id, err)
				return
			}
			_ = m.Cancel(id, "2025-02-03")
			_ = m.Reschedule(id, "2025-03-03", "2025-12-01")
		}(gi)
	}
	// 并发展开只要求不 panic、不竞态、结果可序列化。
	const readers = 8
	for ri := 0; ri < readers; ri++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.Expand("2025-01-01", "2025-10-01")
		}()
	}
	wg.Wait()
	ins, err := m.Expand("2025-01-01", "2025-10-01")
	if err != nil {
		t.Fatal(err)
	}
	// 每个系列：01-06 在原位；02-03 取消；03-03 改到 09-01 不在窗口内 => 每系列 1 条。
	if len(ins) != g {
		t.Fatalf("got %d instances want %d (%v)", len(ins), g, ins)
	}
}
