package runner_test

import (
	"ontology/grants"
	"ontology/runner"
)

func (s *sim) decide(inst, a string, now int64, approve bool) error {
	if inst == "" || a == "" {
		return runner.ErrParam
	}
	if err := s.checkNow(now); err != nil {
		return err
	}
	in, ok := s.insts[inst]
	if !ok {
		return runner.ErrNotFound
	}
	s.expire(in, now)
	if in.state != runner.StateSuspended {
		return runner.ErrState
	}
	if a == in.p {
		return runner.ErrSelf
	}
	if s.masks[a]&grants.ApproveBit == 0 {
		return runner.ErrNoAuthority
	}
	i := in.pending()
	if approve {
		in.steps[i], in.running = runner.StepRunning, i
		in.state, in.miss = runner.StateActive, 0
		in.record(runner.EvOverride, i, 0, a, now)
	} else {
		in.state, in.term, in.termAt = runner.StateFailed, runner.TermRejected, now
		in.record(runner.EvReject, i, 0, a, now)
	}
	s.clock = now
	return nil
}

func (s *sim) status(inst string, now int64) (runner.StatusView, error) {
	if inst == "" {
		return runner.StatusView{}, runner.ErrParam
	}
	if err := s.checkNow(now); err != nil {
		return runner.StatusView{}, err
	}
	in, ok := s.insts[inst]
	if !ok {
		return runner.StatusView{}, runner.ErrNotFound
	}
	v := runner.StatusView{
		State:    in.state,
		Steps:    append([]runner.StepState(nil), in.steps...),
		Miss:     in.miss,
		Deadline: in.deadline,
		Term:     in.term,
		TermAt:   in.termAt,
	}
	if in.state == runner.StateSuspended && in.deadline <= now {
		v.State = runner.StateFailed
		v.Term = runner.TermExpired
		v.TermAt = in.deadline
	}
	return v, nil
}
