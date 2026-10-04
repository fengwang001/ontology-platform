package runner_test

// 朴素模拟：按规则逐位、逐步直写，与 runner 包实现相互独立，
// 用于随机操作序列的对照测试。

import (
	"ontology/flow"
	"ontology/grants"
	"ontology/runner"
)

func bitHas(m uint64, b uint) bool { return m&(uint64(1)<<b) != 0 }

func bitSubset(req, eff uint64) bool {
	for b := uint(0); b < 64; b++ {
		if bitHas(req, b) && !bitHas(eff, b) {
			return false
		}
	}
	return true
}

func bitMiss(req, eff uint64) uint64 {
	var miss uint64
	for b := uint(0); b < 64; b++ {
		if bitHas(req, b) && !bitHas(eff, b) {
			miss |= uint64(1) << b
		}
	}
	return miss
}

type simDef struct {
	ceil uint64
	reqs []uint64
}

type simInst struct {
	p        string
	snap     uint64
	reqs     []uint64
	steps    []runner.StepState
	running  int
	state    runner.InstState
	miss     uint64
	deadline int64
	term     runner.TermKind
	termAt   int64
	audit    []runner.Event
}

func (in *simInst) record(k runner.EvKind, step int, miss uint64, actor string, at int64) {
	in.audit = append(in.audit, runner.Event{
		Seq: len(in.audit) + 1, Kind: k, Step: step, Miss: miss, Actor: actor, At: at,
	})
}

func (in *simInst) pending() int {
	for i, st := range in.steps {
		if st == runner.StepPending {
			return i
		}
	}
	return -1
}

type sim struct {
	masks map[string]uint64
	defs  map[string]simDef
	insts map[string]*simInst
	clock int64
	T     int64
}

func newSim(timeout int64) *sim {
	return &sim{masks: map[string]uint64{}, defs: map[string]simDef{}, insts: map[string]*simInst{}, T: timeout}
}

func (s *sim) checkNow(now int64) error {
	if now < 0 || now > runner.MaxNow {
		return runner.ErrParam
	}
	if now < s.clock {
		return runner.ErrClock
	}
	return nil
}

func (s *sim) expire(in *simInst, now int64) {
	if in.state == runner.StateSuspended && in.deadline <= now {
		in.state = runner.StateFailed
		in.term = runner.TermExpired
		in.termAt = in.deadline
		in.record(runner.EvExpire, in.pending(), 0, "", in.deadline)
	}
}

func (s *sim) grant(p string, m uint64) error {
	if p == "" || m == 0 {
		return grants.ErrParam
	}
	s.masks[p] |= m
	return nil
}

func (s *sim) revoke(p string, m uint64) error {
	if p == "" || m == 0 {
		return grants.ErrParam
	}
	s.masks[p] &^= m
	return nil
}

func (s *sim) define(name string, ceil uint64, reqs []uint64) error {
	if name == "" || bitHas(ceil, 63) || len(reqs) == 0 || len(reqs) > 16 {
		return flow.ErrDef
	}
	for _, q := range reqs {
		if q == 0 || !bitSubset(q, ceil) {
			return flow.ErrDef
		}
	}
	s.defs[name] = simDef{ceil: ceil, reqs: append([]uint64(nil), reqs...)}
	return nil
}

func (s *sim) launch(inst, def, p string, now int64) error {
	if inst == "" || def == "" || p == "" {
		return runner.ErrParam
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	d, ok := s.defs[def]
	if !ok {
		return runner.ErrNotFound
	}
	if _, dup := s.insts[inst]; dup {
		return runner.ErrExists
	}
	s.insts[inst] = &simInst{
		p: p, snap: s.masks[p] & d.ceil, reqs: d.reqs,
		steps: make([]runner.StepState, len(d.reqs)), running: -1,
	}
	s.clock = now
	return nil
}

func (s *sim) startStep(inst string, now int64) (runner.StepOutcome, error) {
	if inst == "" {
		return runner.StepOutcome{}, runner.ErrParam
	}
	if err := s.checkNow(now); err != nil {
		return runner.StepOutcome{}, err
	}
	in, ok := s.insts[inst]
	if !ok {
		return runner.StepOutcome{}, runner.ErrNotFound
	}
	s.expire(in, now)
	i := in.pending()
	if in.state != runner.StateActive || in.running != -1 || i == -1 {
		return runner.StepOutcome{}, runner.ErrState
	}
	eff := s.masks[in.p] & in.snap
	if miss := bitMiss(in.reqs[i], eff); miss != 0 {
		in.state, in.miss, in.deadline = runner.StateSuspended, miss, now+s.T
		in.record(runner.EvDeny, i, miss, "", now)
		s.clock = now
		return runner.StepOutcome{Index: i, Miss: miss}, nil
	}
	in.steps[i], in.running = runner.StepRunning, i
	in.record(runner.EvAllow, i, 0, "", now)
	s.clock = now
	return runner.StepOutcome{Index: i, Allowed: true}, nil
}

func (s *sim) finishStep(inst string, now int64) (int, error) {
	if inst == "" {
		return -1, runner.ErrParam
	}
	if err := s.checkNow(now); err != nil {
		return -1, err
	}
	in, ok := s.insts[inst]
	if !ok {
		return -1, runner.ErrNotFound
	}
	s.expire(in, now)
	if in.state != runner.StateActive || in.running == -1 {
		return -1, runner.ErrState
	}
	i := in.running
	in.steps[i], in.running = runner.StepDone, -1
	if in.pending() == -1 {
		in.state = runner.StateCompleted
	}
	s.clock = now
	return i, nil
}
