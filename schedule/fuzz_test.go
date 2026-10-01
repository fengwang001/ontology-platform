package schedule

import (
	"fmt"
	"math/rand"
	"testing"
)

type op struct {
	kind                     string
	id, date, newID, newDate string
	rule, newRule            Rule
	count                    int
	until                    string
	start                    string
}

func randDate(rng *rand.Rand) string {
	ord := minDate.ord + rng.Intn(maxDate.ord-minDate.ord+1)
	d, _ := dateFromOrd(ord)
	return d.String()
}

func randRule(rng *rand.Rand) Rule {
	nths := []int{1, 2, 3, 4, 5, -1}
	return Rule{K: 1 + rng.Intn(6), Nth: nths[rng.Intn(len(nths))], W: 1 + rng.Intn(7)}
}

func genScript(rng *rand.Rand, steps int) []op {
	var ops []op
	seq := 0
	newID := func() string { seq++; return fmt.Sprintf("s%d", seq) }
	for i := 0; i < steps; i++ {
		// 用两个独立模型同步推进不现实，因此生成时只依赖确定的朴素模型
		switch rng.Intn(12) {
		case 0, 1, 2: // create
			in := op{kind: "create", id: newID(), start: randDate(rng), rule: randRule(rng)}
			if rng.Intn(2) == 0 {
				in.count = 1 + rng.Intn(12)
			} else {
				in.until = randDate(rng)
			}
			ops = append(ops, in)
		case 3, 4: // cancel / reschedule on instance
			if seq == 0 {
				i--
				continue
			}
			ops = append(ops, pickInstanceOp(rng, ops, rng.Intn(2) == 0))
		case 5: // random-date op (大概率非实例日/非法)
			in := op{kind: "cancel", id: fmt.Sprintf("s%d", 1+rng.Intn(seq+1)), date: randDate(rng)}
			if rng.Intn(2) == 0 {
				in.date = []string{"2024-13-01", "2024-02-30", "1900-01-01"}[rng.Intn(3)]
			}
			ops = append(ops, in)
		case 6: // invalid create
			in := op{kind: "create", id: newID(), start: randDate(rng), rule: randRule(rng)}
			switch rng.Intn(5) {
			case 0:
				in.rule.K = 0
			case 1:
				in.rule.Nth = 0
			case 2:
				in.rule.W = 8
			case 3:
				in.count, in.until = 2, "2024-01-01"
			case 4:
				in.start = "bad"
			}
			if in.count == 0 && in.until == "" {
				in.count = 1
			}
			ops = append(ops, in)
		case 7: // reschedule with random/invalid target
			if seq == 0 {
				i--
				continue
			}
			in := pickInstanceOp(rng, ops, false)
			in.newDate = randDate(rng)
			if rng.Intn(4) == 0 {
				in.newDate = "2024-02-30"
			}
			ops = append(ops, in)
		case 8, 9: // split on instance
			if seq == 0 {
				i--
				continue
			}
			in := pickInstanceOp(rng, ops, true)
			in.kind = "split"
			in.newID = newID()
			in.newRule = randRule(rng)
			ops = append(ops, in)
		case 10: // expand
			f := minDate.ord + rng.Intn(maxDate.ord-minDate.ord-3700)
			span := 1 + rng.Intn(3660)
			fd, _ := dateFromOrd(f)
			td, _ := dateFromOrd(f + span)
			ops = append(ops, op{kind: "expand", date: fd.String(), newDate: td.String()})
		case 11: // bad expand
			if rng.Intn(2) == 0 {
				ops = append(ops, op{kind: "expand", date: "2024-05-01", newDate: "2024-05-01"})
			} else {
				ops = append(ops, op{kind: "expand", date: "2000-01-01", newDate: "2011-01-01"})
			}
		}
	}
	return ops
}

// pickInstanceOp 重放已生成的 create（朴素模型内部维护），取一个现存实例日期。
func pickInstanceOp(rng *rand.Rand, ops []op, cancel bool) op {
	nm := newNaive()
	type ref struct{ id string }
	var live []ref
	var chosenDate string
	for _, o := range ops {
		if o.kind == "create" {
			nm.create(CreateInput{ID: o.id, Start: o.start, Rule: o.rule, Count: o.count, Until: o.until})
		}
	}
	for id, s := range nm.series {
		live = append(live, ref{id})
		_ = s
	}
	if len(live) == 0 {
		return op{kind: "cancel", id: "missing", date: "2024-01-01"}
	}
	r := live[rng.Intn(len(live))]
	insts := naiveInstances(nm.series[r.id])
	if len(insts) == 0 {
		chosenDate = randDate(rng)
	} else {
		d, _ := dateFromOrd(insts[rng.Intn(len(insts))])
		chosenDate = d.String()
	}
	if cancel {
		return op{kind: "cancel", id: r.id, date: chosenDate}
	}
	return op{kind: "reschedule", id: r.id, date: chosenDate, newDate: randDate(rng)}
}

func runOpReal(m *Manager, o op) (ErrorCode, []Instance) {
	switch o.kind {
	case "create":
		return code(m.Create(CreateInput{ID: o.id, Start: o.start, Rule: o.rule, Count: o.count, Until: o.until})), nil
	case "cancel":
		return code(m.Cancel(o.id, o.date)), nil
	case "reschedule":
		return code(m.Reschedule(RescheduleInput{ID: o.id, Date: o.date, NewDate: o.newDate})), nil
	case "split":
		_, err := m.Split(SplitInput{ID: o.id, Date: o.date, NewID: o.newID, NewRule: o.newRule})
		return code(err), nil
	case "expand":
		is, err := m.Expand(o.date, o.newDate)
		return code(err), is
	}
	return "", nil
}

func runOpNaive(nm *naiveManager, o op) (ErrorCode, []Instance) {
	switch o.kind {
	case "create":
		return nm.create(CreateInput{ID: o.id, Start: o.start, Rule: o.rule, Count: o.count, Until: o.until}), nil
	case "cancel":
		return nm.cancel(o.id, o.date), nil
	case "reschedule":
		return nm.reschedule(RescheduleInput{ID: o.id, Date: o.date, NewDate: o.newDate}), nil
	case "split":
		return nm.split(SplitInput{ID: o.id, Date: o.date, NewID: o.newID, NewRule: o.newRule}), nil
	case "expand":
		return nm.expand(o.date, o.newDate)
	}
	return "", nil
}

func TestDifferentialAgainstNaive(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genScript(rng, 300)
		real, nm := NewManager(), newNaive()
		for i, o := range ops {
			c1, r1 := runOpReal(real, o)
			c2, r2 := runOpNaive(nm, o)
			if c1 != c2 {
				t.Fatalf("seed=%d op#%d %+v: real code=%q naive=%q", seed, i, o, c1, c2)
			}
			if c1 == "" && o.kind == "expand" && instStr(r1) != instStr(r2) {
				t.Fatalf("seed=%d op#%d expand mismatch:\nreal=%s\nnaive=%s", seed, i, instStr(r1), instStr(r2))
			}
			if i%25 == 0 {
				t.Logf("seed=%d op#%d kind=%s 输入 id=%s date=%s => code=%q 实例数=%d", seed, i, o.kind, o.id, o.date, c1, len(r1))
			}
		}
		// 全时间轴滑动窗口再对一遍展开结果（3660 天/窗，步长 1830 天）
		for f := minDate.ord; f < maxDate.ord; f += 1830 {
			t2 := f + 3660
			if t2 > maxDate.ord {
				t2 = maxDate.ord
			}
			fd, _ := dateFromOrd(f)
			td, _ := dateFromOrd(t2)
			_, r1 := runOpReal(real, op{kind: "expand", date: fd.String(), newDate: td.String()})
			_, r2 := runOpNaive(nm, op{kind: "expand", date: fd.String(), newDate: td.String()})
			if instStr(r1) != instStr(r2) {
				t.Fatalf("seed=%d window %s..%s mismatch:\nreal=%s\nnaive=%s", seed, fd.String(), td.String(), instStr(r1), instStr(r2))
			}
		}
		t.Logf("seed=%d 判定依据：300 个随机操作的错误码与每次展开均与逐日扫描朴素模型一致", seed)
	}
}

// 相同操作序列重放 => 完全相同的展开结果。
func TestReplayDeterministic(t *testing.T) {
	ops := genScript(rand.New(rand.NewSource(777)), 200)
	run := func() []Instance {
		m := NewManager()
		var all []Instance
		for _, o := range ops {
			_, is := runOpReal(m, o)
			all = append(all, is...)
		}
		return all
	}
	a, b := run(), run()
	if instStr(a) != instStr(b) {
		t.Fatal("replay produced different results")
	}
	// 全跨度窗口必须被拒绝（跨度远超 3660 天）
	m := NewManager()
	for _, o := range ops {
		runOpReal(m, o)
	}
	fd, _ := dateFromOrd(minDate.ord)
	td, _ := dateFromOrd(maxDate.ord)
	_, err := m.Expand(fd.String(), td.String())
	expectCode(t, err, ErrInvalidRange, "full span must be rejected")
	t.Logf("判定依据：全跨度 %d 天 > 3660，整体展开按 invalid_expand_range 拒绝", td.ord-fd.ord)
	// 合法滑窗的展开两次重放一致
	for f := minDate.ord; f < maxDate.ord; f += 1830 {
		t2 := f + 3660
		if t2 > maxDate.ord {
			t2 = maxDate.ord
		}
		a, _ := dateFromOrd(f)
		b, _ := dateFromOrd(t2)
		r1, e1 := m.Expand(a.String(), b.String())
		r2, e2 := m.Expand(a.String(), b.String())
		if e1 != nil || e2 != nil || instStr(r1) != instStr(r2) {
			t.Fatal("sliding-window expand not deterministic")
		}
	}
	t.Logf("判定依据：同一操作序列重放两次，逐次展开与滑窗展开均完全相同")
}
