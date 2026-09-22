package saga

import (
	"errors"

	"ontology/compens"
	"ontology/journal"
	"ontology/step"
)

// drive 是 Run/Resume 共用的推进器；从日志重建当前进度后继续执行。
func (o *Orchestrator) drive(id string, it *inst) (State, error) {
	recs := o.journal.Read(id)
	succeeded := map[int]bool{}
	phaseForward := true
	for _, r := range recs {
		if r.Direction == journal.Forward && r.Result == journal.Success {
			succeeded[r.StepIndex] = true
		}
		if r.Direction == journal.Compensate {
			phaseForward = false
		}
	}

	var runErr error
	if phaseForward {
		start := 0
		for k := range succeeded {
			if k >= start {
				start = k + 1
			}
		}
		needComp, err := o.runForward(id, it, start)
		if needComp {
			succeeded = o.forwardSuccessSet(id)
			if cerr := o.runCompensate(id, it, succeeded); cerr != nil {
				runErr = cerr
			} else if err != nil {
				runErr = err
			}
		}
	} else {
		succeeded = o.forwardSuccessSet(id)
		if cerr := o.runCompensate(id, it, succeeded); cerr != nil {
			runErr = cerr
		}
	}

	it.cached = o.snapshotLocked(id, it)
	return it.cached, runErr
}

func (o *Orchestrator) forwardSuccessSet(id string) map[int]bool {
	out := map[int]bool{}
	for _, r := range o.journal.Read(id) {
		if r.Direction == journal.Forward && r.Result == journal.Success {
			out[r.StepIndex] = true
		}
	}
	return out
}

// runForward 顺序执行 start..n-1。返回是否需要进入补偿（明确失败或出现未知结果）。
func (o *Orchestrator) runForward(id string, it *inst, start int) (bool, error) {
	for i := start; i < len(it.steps); i++ {
		s := it.steps[i]
		res, unknown, err := o.attempt(it, s, true)
		rec := journal.Record{
			InstanceID: id, StepIndex: i, StepKey: s.Key,
			Direction: journal.Forward, At: o.now(),
		}
		if res == journal.Success {
			rec.Result, rec.Unknown = journal.Success, unknown
			if _, aerr := o.journal.Append(rec); aerr != nil {
				return true, mapJournalError(aerr)
			}
			if unknown {
				return true, nil
			}
			continue
		}
		rec.Result, rec.Detail = journal.Failure, errMsg(err)
		if _, aerr := o.journal.Append(rec); aerr != nil {
			return true, mapJournalError(aerr)
		}
		return true, err
	}
	return false, nil
}

// runCompensate 对已成功步骤严格逆序补偿；补偿失败也继续执行剩余补偿。
func (o *Orchestrator) runCompensate(id string, it *inst, succeeded map[int]bool) error {
	plan, err := compens.Build(it.steps, succeeded)
	if err != nil {
		if errors.Is(err, compens.ErrNilCompensate) {
			return ErrNilCompensate
		}
		return err
	}
	recs := o.journal.Read(id)
	done := map[int]bool{}
	for _, r := range recs {
		if r.Direction == journal.Compensate && r.Result == journal.Success {
			done[r.StepIndex] = true
		}
	}
	var failures []error
	for _, item := range plan.Items {
		if done[item.Index] {
			continue
		}
		s := item.Step
		res, _, ferr := o.attempt(it, s, false)
		rec := journal.Record{
			InstanceID: id, StepIndex: item.Index, StepKey: s.Key,
			Direction: journal.Compensate, At: o.now(),
		}
		if res == journal.Success {
			rec.Result = journal.Success
		} else {
			rec.Result, rec.Detail = journal.Failure, errMsg(ferr)
		}
		if _, aerr := o.journal.Append(rec); aerr != nil {
			return mapJournalError(aerr)
		}
		if res == journal.Failure {
			failures = append(failures, ferr)
		}
	}
	return errors.Join(failures...)
}

// attempt 真实调用一次动作（含重试），返回落盘结果。
// unknown 仅正向可能为 true；未知按成功落盘且不重试。
func (o *Orchestrator) attempt(it *inst, s step.Step, forward bool) (journal.Result, bool, error) {
	action := s.Forward
	counter := s.Key
	if !forward {
		action = s.Compensate
	}
	tries := 1
	if s.Retryable {
		tries = o.cfg.MaxRetries
	}
	var lastErr error
	for t := 0; t < tries; t++ {
		if action != nil {
			it.calls.record(counter, forward)
			lastErr = action()
		} else {
			lastErr = nil
		}
		if lastErr == nil {
			return journal.Success, false, nil
		}
		if step.IsUnknown(lastErr) {
			if forward {
				return journal.Success, true, lastErr
			}
			return journal.Success, false, nil
		}
	}
	return journal.Failure, false, lastErr
}

func (c *Calls) record(key string, forward bool) {
	if forward {
		c.Forward[key]++
		c.ForwardAll++
	} else {
		c.Compensate[key]++
		c.CompensateAll++
	}
}
