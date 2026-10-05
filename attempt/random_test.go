package attempt_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/attempt"
	"ontology/fanout"
	"ontology/matrix"
)

// ---------- 朴素模拟：按规格逐步写成，与被测实现相互独立 ----------

type naiveCombo struct {
	axis  []string
	extra map[string]string
	ord   []string
}

func naiveExperimental(pairs []matrix.Pair) bool {
	for _, p := range pairs {
		if p.Key == "experimental" && p.Value == "true" {
			return true
		}
	}
	return false
}

func naiveExpand(cfg matrix.Config) ([]matrix.Job, error) {
	idx := map[string]int{}
	for i, ax := range cfg.Axes {
		idx[ax.Key] = i
	}
	// 第一步：笛卡尔积，首轴变化最慢。
	combos := []naiveCombo{{axis: make([]string, len(cfg.Axes)), extra: map[string]string{}}}
	for ai, ax := range cfg.Axes {
		var next []naiveCombo
		for _, c := range combos {
			for _, v := range ax.Values {
				nc := naiveCombo{axis: append([]string(nil), c.axis...), extra: map[string]string{}}
				nc.axis[ai] = v
				next = append(next, nc)
			}
		}
		combos = next
	}
	// 第二步：exclude。
	var surv []naiveCombo
excluded:
	for _, c := range combos {
		for _, ex := range cfg.Exclude {
			match := true
			for k, v := range ex {
				if c.axis[idx[k]] != v {
					match = false
					break
				}
			}
			if match {
				continue excluded
			}
		}
		surv = append(surv, c)
	}
	// 第三步：include 逐项处理。
	var appended []matrix.Job
	for _, inc := range cfg.Include {
		var addKeys []string
		for k := range inc {
			if _, isAxis := idx[k]; !isAxis {
				addKeys = append(addKeys, k)
			}
		}
		sort.Strings(addKeys)
		matched := false
		for si := range surv {
			ok := true
			for k, v := range inc {
				if ai, isAxis := idx[k]; isAxis && surv[si].axis[ai] != v {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			matched = true
			for _, k := range addKeys {
				if _, has := surv[si].extra[k]; !has {
					surv[si].ord = append(surv[si].ord, k)
				}
				surv[si].extra[k] = inc[k]
			}
		}
		if !matched {
			var pairs []matrix.Pair
			for _, ax := range cfg.Axes {
				if v, ok := inc[ax.Key]; ok {
					pairs = append(pairs, matrix.Pair{Key: ax.Key, Value: v})
				}
			}
			for _, k := range addKeys {
				pairs = append(pairs, matrix.Pair{Key: k, Value: inc[k]})
			}
			appended = append(appended, matrix.Job{Pairs: pairs, Experimental: naiveExperimental(pairs)})
		}
	}
	var jobs []matrix.Job
	for _, c := range surv {
		var pairs []matrix.Pair
		for i, ax := range cfg.Axes {
			pairs = append(pairs, matrix.Pair{Key: ax.Key, Value: c.axis[i]})
		}
		for _, k := range c.ord {
			pairs = append(pairs, matrix.Pair{Key: k, Value: c.extra[k]})
		}
		jobs = append(jobs, matrix.Job{Pairs: pairs, Experimental: naiveExperimental(pairs)})
	}
	jobs = append(jobs, appended...)
	if len(jobs) > matrix.MaxJobs {
		return nil, matrix.ErrTooLarge
	}
	if len(jobs) == 0 {
		return nil, matrix.ErrEmpty
	}
	return jobs, nil
}

type naiveExec struct {
	states   []fanout.State
	exp      []bool
	p        int
	failFast bool
	a        int
	rounds   int
	started  bool
	runs     []int
}

func newNaiveExec(jobs []matrix.Job, p int, failFast bool, a int) *naiveExec {
	exp := make([]bool, len(jobs))
	for i, j := range jobs {
		exp[i] = j.Experimental
	}
	return &naiveExec{
		states:   make([]fanout.State, len(jobs)),
		exp:      exp,
		p:        p,
		failFast: failFast,
		a:        a,
		runs:     make([]int, len(jobs)),
	}
}

func (n *naiveExec) terminal() bool {
	if !n.started {
		return false
	}
	for _, s := range n.states {
		if s == fanout.Running || s == fanout.Pending {
			return false
		}
	}
	return true
}

func (n *naiveExec) result() fanout.Result {
	cancelled := false
	for i, s := range n.states {
		if s == fanout.Failed && !n.exp[i] {
			return fanout.ResultFailed
		}
		if s == fanout.Cancelled {
			cancelled = true
		}
	}
	if cancelled {
		return fanout.ResultCancelled
	}
	return fanout.ResultSucceeded
}

func (n *naiveExec) start() error {
	if n.started {
		return fanout.ErrState
	}
	n.started = true
	n.rounds++
	k := min(n.p, len(n.states))
	for i := 0; i < k; i++ {
		n.states[i] = fanout.Running
		n.runs[i]++
	}
	return nil
}

func (n *naiveExec) finish(i int, ok bool) error {
	if i < 0 || i >= len(n.states) {
		return fanout.ErrParam
	}
	if n.states[i] != fanout.Running {
		return fanout.ErrState
	}
	if ok {
		n.states[i] = fanout.Succeeded
	} else {
		n.states[i] = fanout.Failed
	}
	if !ok && n.failFast && !n.exp[i] {
		for j := range n.states {
			if n.states[j] == fanout.Running || n.states[j] == fanout.Pending {
				n.states[j] = fanout.Cancelled
			}
		}
		return nil
	}
	// 朴素线性扫描：启动下标最小的 Pending。
	for j := range n.states {
		if n.states[j] == fanout.Pending {
			n.states[j] = fanout.Running
			n.runs[j]++
			break
		}
	}
	return nil
}

func (n *naiveExec) cancelAll() {
	if !n.started || n.terminal() {
		return
	}
	for j := range n.states {
		if n.states[j] == fanout.Running || n.states[j] == fanout.Pending {
			n.states[j] = fanout.Cancelled
		}
	}
}

func (n *naiveExec) rerun() error {
	if !n.terminal() || n.result() == fanout.ResultSucceeded {
		return fanout.ErrState
	}
	if n.rounds >= n.a {
		return attempt.ErrRerunLimit
	}
	n.rounds++
	for i := range n.states {
		if n.states[i] != fanout.Succeeded {
			n.states[i] = fanout.Pending
		}
	}
	started := 0
	for i := range n.states {
		if started >= n.p {
			break
		}
		if n.states[i] == fanout.Pending {
			n.states[i] = fanout.Running
			n.runs[i]++
			started++
		}
	}
	return nil
}

// ---------- 随机配置与操作序列生成 ----------

func randMatrixConfig(rng *rand.Rand) matrix.Config {
	axisKeys := []string{"os", "ver", "arch", "experimental"}
	vals := []string{"1", "2", "3", "true", "x"}
	perm := rng.Perm(len(axisKeys))
	var cfg matrix.Config
	for _, ai := range perm[:1+rng.Intn(3)] {
		vp := rng.Perm(len(vals))
		var vs []string
		for _, vi := range vp[:1+rng.Intn(3)] {
			vs = append(vs, vals[vi])
		}
		cfg.Axes = append(cfg.Axes, matrix.Axis{Key: axisKeys[ai], Values: vs})
	}
	pickVal := func() string { return vals[rng.Intn(len(vals))] }
	for i := 0; i < rng.Intn(3); i++ {
		item := map[string]string{}
		for _, ax := range cfg.Axes {
			if rng.Float64() < 0.5 {
				item[ax.Key] = pickVal()
			}
		}
		if len(item) == 0 {
			item[cfg.Axes[0].Key] = pickVal()
		}
		cfg.Exclude = append(cfg.Exclude, item)
	}
	extraKeys := []string{"tag", "experimental", "extra"}
	for i := 0; i < rng.Intn(4); i++ {
		item := map[string]string{}
		for _, ax := range cfg.Axes {
			if rng.Float64() < 0.4 {
				item[ax.Key] = pickVal()
			}
		}
		for _, k := range extraKeys {
			if rng.Float64() < 0.3 {
				item[k] = pickVal()
			}
		}
		if len(item) == 0 {
			item[extraKeys[rng.Intn(len(extraKeys))]] = pickVal()
		}
		cfg.Include = append(cfg.Include, item)
	}
	return cfg
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, matrix.ErrInvalid):
		return "invalid"
	case errors.Is(err, matrix.ErrTooLarge):
		return "too-large"
	case errors.Is(err, matrix.ErrEmpty):
		return "empty"
	case errors.Is(err, fanout.ErrParam):
		return "param"
	case errors.Is(err, fanout.ErrState):
		return "state"
	case errors.Is(err, attempt.ErrRerunLimit):
		return "rerun-limit"
	}
	return "unknown"
}

// 1500 组随机配置与操作序列，与朴素模拟逐步对照；同一操作序列在两个
// 执行器实例上重放，验证结果相同。日志打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const total = 1500
	for caseNo := 0; caseNo < total; caseNo++ {
		cfg := attempt.Config{
			Matrix:   randMatrixConfig(rng),
			P:        1 + rng.Intn(4),
			FailFast: rng.Float64() < 0.5,
			A:        1 + rng.Intn(3),
		}
		log := &strings.Builder{}
		fmt.Fprintf(log, "case %d 输入: %+v\n", caseNo, cfg)

		wantJobs, wantErr := naiveExpand(cfg.Matrix)
		e1, err := attempt.New(cfg)
		fmt.Fprintf(log, "展开: 实现=%s 朴素=%s\n", errClass(err), errClass(wantErr))
		if errClass(err) != errClass(wantErr) {
			t.Fatalf("%s展开错误类不一致", log)
		}
		if err != nil {
			t.Logf("%s判定: 双方同报 %s", log, errClass(err))
			continue
		}
		if !reflect.DeepEqual(e1.Jobs(), wantJobs) {
			t.Fatalf("%s展开不一致:\n实现 %+v\n朴素 %+v", log, e1.Jobs(), wantJobs)
		}
		// 相同配置再展开一次，逐字节相同。
		e2, err := attempt.New(cfg)
		if err != nil || !reflect.DeepEqual(e1.Jobs(), e2.Jobs()) {
			t.Fatalf("%s相同配置展开结果不一致", log)
		}

		nv := newNaiveExec(wantJobs, cfg.P, cfg.FailFast, cfg.A)
		n := len(wantJobs)
		ops := 0
		apply := func(name string, err1, err2, errN error) {
			t.Helper()
			ops++
			fmt.Fprintf(log, "op %d %s: 实现1=%s 实现2=%s 朴素=%s\n", ops, name, errClass(err1), errClass(err2), errClass(errN))
			if errClass(err1) != errClass(errN) || errClass(err2) != errClass(errN) {
				t.Fatalf("%s操作结果不一致", log)
			}
			for i := 0; i < n; i++ {
				if e1.State(i) != nv.states[i] || e2.State(i) != nv.states[i] {
					t.Fatalf("%s作业 %d 状态不一致: 实现1=%s 实现2=%s 朴素=%s",
						log, i, e1.State(i), e2.State(i), nv.states[i])
				}
				if e1.Runs(i) != nv.runs[i] || e2.Runs(i) != nv.runs[i] {
					t.Fatalf("%s作业 %d 运行次数不一致", log, i)
				}
			}
			if e1.Terminal() != nv.terminal() || e2.Terminal() != nv.terminal() {
				t.Fatalf("%s终结判定不一致", log)
			}
			if nv.terminal() && (e1.Result() != nv.result() || e2.Result() != nv.result()) {
				t.Fatalf("%s结果不一致: 实现1=%s 实现2=%s 朴素=%s",
					log, e1.Result(), e2.Result(), nv.result())
			}
			if e1.Rounds() != nv.rounds || e2.Rounds() != nv.rounds {
				t.Fatalf("%s轮次不一致", log)
			}
		}
		apply("Start", e1.Start(), e2.Start(), nv.start())
		for s := 0; s < 40; s++ {
			r := rng.Float64()
			switch {
			case r < 0.60: // Finish 一个 Running 作业（若有）
				var running []int
				for i, st := range nv.states {
					if st == fanout.Running {
						running = append(running, i)
					}
				}
				if len(running) == 0 {
					i := rng.Intn(n + 1)
					ok := rng.Float64() < 0.7
					apply(fmt.Sprintf("Finish(%d,%v)", i, ok), e1.Finish(i, ok), e2.Finish(i, ok), nv.finish(i, ok))
				} else {
					i := running[rng.Intn(len(running))]
					ok := rng.Float64() < 0.7
					apply(fmt.Sprintf("Finish(%d,%v)", i, ok), e1.Finish(i, ok), e2.Finish(i, ok), nv.finish(i, ok))
				}
			case r < 0.70: // 随机下标 Finish，可能越界或状态不符
				i := rng.Intn(n+2) - 1
				ok := rng.Float64() < 0.5
				apply(fmt.Sprintf("Finish(%d,%v)", i, ok), e1.Finish(i, ok), e2.Finish(i, ok), nv.finish(i, ok))
			case r < 0.85: // Rerun
				apply("Rerun", e1.Rerun(), e2.Rerun(), nv.rerun())
			case r < 0.90: // CancelAll
				e1.CancelAll()
				e2.CancelAll()
				nv.cancelAll()
				apply("CancelAll", nil, nil, nil)
			default: // 重复 Start
				apply("Start", e1.Start(), e2.Start(), nv.start())
			}
		}
		fmt.Fprintf(log, "输出: jobs=%d terminal=%v result=%s rounds=%d\n",
			n, nv.terminal(), nv.result(), nv.rounds)
		t.Logf("%s判定: 展开、%d 个操作的状态/运行次数/轮次/终结结果与朴素模拟逐步一致，双实例重放一致",
			log, ops)
	}
}

// 并发调用等价于某个串行顺序：不 panic、不变式保持、race 检测通过。
func TestConcurrent(t *testing.T) {
	cfg := attempt.Config{
		Matrix: matrix.Config{
			Axes: []matrix.Axis{
				{Key: "os", Values: []string{"a", "b", "c", "d"}},
				{Key: "ver", Values: []string{"1", "2", "3"}},
			},
			Include: []map[string]string{{"ver": "1", "experimental": "true"}},
		},
		P:        4,
		FailFast: true,
		A:        3,
	}
	e, err := attempt.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				switch rng.Intn(5) {
				case 0, 1, 2:
					_ = e.Finish(rng.Intn(e.N()), rng.Float64() < 0.6)
				case 3:
					_ = e.Rerun()
				case 4:
					e.CancelAll()
				}
			}
		}(int64(g))
	}
	wg.Wait()
	running := 0
	for i := 0; i < e.N(); i++ {
		if e.State(i) == fanout.Running {
			running++
		}
	}
	if running > cfg.P {
		t.Fatalf("running=%d exceeds P=%d", running, cfg.P)
	}
	if e.Rounds() > cfg.A {
		t.Fatalf("rounds=%d exceeds A=%d", e.Rounds(), cfg.A)
	}
}
