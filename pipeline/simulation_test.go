package pipeline

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/dag"
	"ontology/rule"
)

// naiveSim 是按规格书写的逐步朴素模拟：每次操作后全图扫描，
// 把所有可评估的 Created 作业评估至不动点。它与 Pipeline 的
// BFS 级联相互独立，用于交叉验证最终状态与评估顺序无关。
type naiveSim struct {
	graph   *dag.Graph
	order   []string
	spec    map[string]dag.Job
	when    map[string]rule.When
	state   map[string]State
	retries map[string]int
	evals   int
}

func newNaiveSim(t *testing.T, specs []dag.Job) *naiveSim {
	t.Helper()
	g, err := dag.New(specs)
	if err != nil {
		t.Fatalf("dag.New() = %v", err)
	}
	s := &naiveSim{
		graph:   g,
		spec:    make(map[string]dag.Job),
		when:    make(map[string]rule.When),
		state:   make(map[string]State),
		retries: make(map[string]int),
	}
	for _, spec := range specs {
		s.order = append(s.order, spec.Name)
		s.spec[spec.Name] = spec
		w, _ := rule.Parse(spec.When)
		s.when[spec.Name] = w
		s.state[spec.Name] = StateCreated
	}
	s.settle()
	return s
}

func (s *naiveSim) settled(name string) bool {
	st := s.state[name]
	return st.Terminal() || (st == StateManual && s.spec[name].AllowFailure)
}

// settle 全图扫描至不动点：每轮把全部 needs 已定的 Created 作业评估一次。
func (s *naiveSim) settle() {
	for changed := true; changed; {
		changed = false
		for _, name := range s.order {
			if s.state[name] != StateCreated {
				continue
			}
			allSettled := true
			for _, need := range s.spec[name].Needs {
				if !s.settled(need) {
					allSettled = false
					break
				}
			}
			if !allSettled {
				continue
			}
			s.evaluate(name)
			changed = true
		}
	}
}

// evaluate 评估单个作业，返回判定依据（bad/skip）供日志使用。
func (s *naiveSim) evaluate(name string) (bad, skip bool) {
	for _, need := range s.spec[name].Needs {
		switch st := s.state[need]; {
		case st == StateFailed && !s.spec[need].AllowFailure:
			bad = true
		case st == StateSkipped || st == StateCanceled:
			skip = true
		}
	}
	switch rule.Evaluate(s.when[name], bad, skip) {
	case rule.ToPending:
		s.state[name] = StatePending
	case rule.ToSkipped:
		s.state[name] = StateSkipped
	case rule.ToManual:
		s.state[name] = StateManual
	}
	s.evals++
	return bad, skip
}

func (s *naiveSim) lookup(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty job name", ErrInvalidArgument)
	}
	if _, ok := s.spec[name]; !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return nil
}

func (s *naiveSim) start(name string) error {
	if err := s.lookup(name); err != nil {
		return err
	}
	if s.state[name] != StatePending {
		return fmt.Errorf("%w: start", ErrInvalidState)
	}
	s.state[name] = StateRunning
	return nil
}

func (s *naiveSim) finish(name string, ok bool) error {
	if err := s.lookup(name); err != nil {
		return err
	}
	if s.state[name] != StateRunning {
		return fmt.Errorf("%w: finish", ErrInvalidState)
	}
	switch {
	case ok:
		s.state[name] = StateSuccess
	case s.retries[name] < s.spec[name].Retry:
		s.retries[name]++
		s.state[name] = StatePending
		return nil // 重试未耗尽：不评估下游
	default:
		s.state[name] = StateFailed
	}
	s.settle()
	return nil
}

func (s *naiveSim) play(name string) error {
	if err := s.lookup(name); err != nil {
		return err
	}
	if s.state[name] != StateManual {
		return fmt.Errorf("%w: play", ErrInvalidState)
	}
	s.state[name] = StatePending
	return nil
}

func (s *naiveSim) cancel() {
	for _, name := range s.order {
		if !s.state[name].Terminal() {
			s.state[name] = StateCanceled
		}
	}
}

func (s *naiveSim) retryJob(name string) error {
	if err := s.lookup(name); err != nil {
		return err
	}
	if s.state[name] != StateFailed && s.state[name] != StateCanceled {
		return fmt.Errorf("%w: retryJob", ErrInvalidState)
	}
	closure := s.graph.TransitiveDownstream(name)
	for nxt := range closure {
		if s.state[nxt] == StateRunning {
			return fmt.Errorf("%w: downstream running", ErrInvalidState)
		}
	}
	s.state[name] = StatePending
	s.retries[name] = 0
	for nxt := range closure {
		s.state[nxt] = StateCreated
		s.retries[nxt] = 0
	}
	s.settle()
	return nil
}

func (s *naiveSim) status() (Status, int) {
	var running, blocked, failed, canceled bool
	var warnings int
	for _, name := range s.order {
		switch s.state[name] {
		case StateRunning, StatePending:
			running = true
		case StateManual:
			if !s.spec[name].AllowFailure {
				blocked = true
			}
		case StateFailed:
			if s.spec[name].AllowFailure {
				warnings++
			} else {
				failed = true
			}
		case StateCanceled:
			canceled = true
		}
	}
	switch {
	case running:
		return StatusRunning, 0
	case blocked:
		return StatusBlocked, 0
	case failed:
		return StatusFailed, 0
	case canceled:
		return StatusCanceled, 0
	}
	return StatusSuccess, warnings
}

// randOp 是一个随机操作。
type randOp struct {
	kind string // start / finishOk / finishFail / play / cancel / retry / status
	name string
}

func (op randOp) String() string {
	return fmt.Sprintf("%s(%s)", op.kind, op.name)
}

var whens = []string{"on_success", "on_failure", "always", "manual"}

// genSpecs 生成随机合法 DAG：needs 只取自下标更小的作业，天然无环。
func genSpecs(rng *rand.Rand) []dag.Job {
	n := 1 + rng.Intn(10)
	specs := make([]dag.Job, 0, n)
	for i := 0; i < n; i++ {
		spec := dag.Job{
			Name:         fmt.Sprintf("j%d", i),
			When:         whens[rng.Intn(len(whens))],
			AllowFailure: rng.Intn(10) < 3,
			Retry:        rng.Intn(3),
		}
		for k := 0; k < i; k++ {
			if rng.Intn(100) < 30 {
				spec.Needs = append(spec.Needs, fmt.Sprintf("j%d", k))
			}
		}
		specs = append(specs, spec)
	}
	return specs
}

// nextOp 依据当前模拟状态生成一个随机操作：约七成针对当前状态
// 合理的操作，三成完全随机（含空名与未知名）以覆盖拒绝路径。
func nextOp(rng *rand.Rand, sim *naiveSim, names []string) randOp {
	name := names[rng.Intn(len(names))]
	if rng.Intn(10) < 7 {
		if _, ok := sim.spec[name]; ok {
			switch sim.state[name] {
			case StatePending:
				return randOp{kind: "start", name: name}
			case StateRunning:
				if rng.Intn(2) == 0 {
					return randOp{kind: "finishOk", name: name}
				}
				return randOp{kind: "finishFail", name: name}
			case StateManual:
				return randOp{kind: "play", name: name}
			case StateFailed, StateCanceled:
				return randOp{kind: "retry", name: name}
			}
		}
	}
	switch rng.Intn(8) {
	case 0:
		return randOp{kind: "start", name: name}
	case 1:
		return randOp{kind: "finishOk", name: name}
	case 2:
		return randOp{kind: "finishFail", name: name}
	case 3:
		return randOp{kind: "play", name: name}
	case 4:
		return randOp{kind: "retry", name: name}
	case 5:
		return randOp{kind: "cancel"}
	default:
		return randOp{kind: "status"}
	}
}

func applyOp(p *Pipeline, op randOp) error {
	switch op.kind {
	case "start":
		return p.Start(op.name)
	case "finishOk":
		return p.Finish(op.name, true)
	case "finishFail":
		return p.Finish(op.name, false)
	case "play":
		return p.Play(op.name)
	case "cancel":
		p.Cancel()
		return nil
	case "retry":
		return p.RetryJob(op.name)
	}
	return nil
}

func applyOpSim(s *naiveSim, op randOp) error {
	switch op.kind {
	case "start":
		return s.start(op.name)
	case "finishOk":
		return s.finish(op.name, true)
	case "finishFail":
		return s.finish(op.name, false)
	case "play":
		return s.play(op.name)
	case "cancel":
		s.cancel()
		return nil
	case "retry":
		return s.retryJob(op.name)
	}
	return nil
}

// errClass 把错误归入可比较的类别：0 无错，1 参数非法，2 不存在，3 状态不符。
func errClass(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrInvalidArgument):
		return 1
	case errors.Is(err, ErrNotFound):
		return 2
	case errors.Is(err, ErrInvalidState):
		return 3
	}
	return -1
}

// TestRandomAgainstNaiveSimulation 用 1500 组随机图与操作序列，
// 把 Pipeline 与逐步朴素模拟逐步对照：每一步比较错误类别、
// 全部作业状态、整体状态与 Warnings，最后比较评估总次数。
func TestRandomAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for tc := 0; tc < 1500; tc++ {
		specs := genSpecs(rng)
		p, err := New(specs)
		if err != nil {
			t.Fatalf("case %d: New() = %v", tc, err)
		}
		sim := newNaiveSim(t, specs)
		names := append(append([]string{}, sim.order...), "", "ghost")
		t.Logf("case %d input: specs=%v", tc, specs)
		for i := 0; i < 30; i++ {
			op := nextOp(rng, sim, names)
			t.Logf("case %d step %d: %s", tc, i, op)
			errP := applyOp(p, op)
			errS := applyOpSim(sim, op)
			if errClass(errP) != errClass(errS) {
				t.Fatalf("case %d step %d %s: err class %d (pipeline) != %d (sim): %v vs %v",
					tc, i, op, errClass(errP), errClass(errS), errP, errS)
			}
			if got, want := p.Snapshot(), sim.state; !reflect.DeepEqual(got, want) {
				t.Fatalf("case %d step %d %s: states differ\npipeline: %v\nsim:      %v",
					tc, i, op, got, want)
			}
			stP, wP := p.Status()
			stS, wS := sim.status()
			if stP != stS || wP != wS {
				t.Fatalf("case %d step %d %s: status (%v,%d) != (%v,%d)",
					tc, i, op, stP, wP, stS, wS)
			}
		}
		if p.evals != sim.evals {
			t.Fatalf("case %d: evals %d (pipeline) != %d (sim)", tc, p.evals, sim.evals)
		}
		stP, wP := p.Status()
		t.Logf("case %d output: states=%v status=%v warnings=%d evals=%d (判定依据: 逐步对照朴素模拟一致)",
			tc, p.Snapshot(), stP, wP, p.evals)
	}
}
