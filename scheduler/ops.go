package scheduler

import "fmt"

func (s *Scheduler) AddTask(dur int64) (int, *UpdateReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if dur < 0 || dur > 1_000_000 {
		return 0, nil, fmt.Errorf("%w: duration must be in [0,1000000]", ErrInvalidArgument)
	}
	if s.taskCount >= s.taskLimit {
		return 0, nil, ErrTaskLimit
	}

	oldPF := s.currentPF()
	v := s.taskCount
	s.duration = append(s.duration, dur)
	s.snet = append(s.snet, 0)
	s.fnlt = append(s.fnlt, -1)
	s.es = append(s.es, 0)
	s.ef = append(s.ef, dur)
	s.lf = append(s.lf, s.deadline)
	s.ls = append(s.ls, s.deadline-dur)
	s.tf = append(s.tf, s.deadline-dur)
	s.preds = append(s.preds, make([]int, 0, 2))
	s.succs = append(s.succs, make([]int, 0, 2))
	s.critical = append(s.critical, false)
	minTF, hasOld := s.minimumFromTFCount()
	s.tfCount[s.deadline-dur]++
	if !hasOld || s.deadline-dur < minTF {
		s.minTF = minTF
	}
	s.taskCount++
	s.order = append(s.order, v)
	s.pos = append(s.pos, v)
	s.fwdEval, s.bwdEval = 1, 1

	if dur > s.pf {
		s.pf = dur
	}
	added, removed := s.refreshCriticalForAddTask(v, minTF, hasOld)
	return v, s.report(nil, nil, oldPF, added, removed), nil
}

func (s *Scheduler) AddDep(u, v int, lag int64) (*UpdateReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if lag < -1_000_000 || lag > 1_000_000 {
		return nil, fmt.Errorf("%w: lag must be in [-1000000,1000000]", ErrInvalidArgument)
	}
	if !s.taskExists(u) || !s.taskExists(v) {
		return nil, ErrTaskNotFound
	}
	if s.edgeExists(u, v) {
		return nil, ErrDepExists
	}
	if s.edgeCount >= s.edgeLimit {
		return nil, ErrDepLimit
	}
	if u == v || s.wouldCycle(u, v) {
		return nil, ErrCycle
	}

	oldCrit := s.snapshotCritical()
	oldPF := s.currentPF()
	s.fwdEval, s.bwdEval = 0, 0
	s.moveAfter(u, v)
	s.insertEdge(u, v, lag)
	s.edgeCount++

	changedES := s.forwardSeeds([]int{v})
	changedLF := s.backwardSeeds([]int{u})
	s.updatePF(mergeInts([]int{u, v}, changedES))
	s.refreshTFs(mergeInts([]int{u, v}, changedES, changedLF))
	added, removed := s.refreshCritical(oldCrit)
	return s.report(changedES, changedLF, oldPF, added, removed), nil
}

func (s *Scheduler) SetDuration(v int, dur int64) (*UpdateReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if dur < 0 || dur > 1_000_000 {
		return nil, fmt.Errorf("%w: duration must be in [0,1000000]", ErrInvalidArgument)
	}
	if !s.taskExists(v) {
		return nil, ErrTaskNotFound
	}

	oldCrit := s.snapshotCritical()
	oldPF := s.currentPF()
	s.fwdEval, s.bwdEval = 0, 0
	var changedES, changedLF []int
	if s.duration[v] != dur {
		s.duration[v] = dur
		s.ef[v] = s.es[v] + dur

		changedES = s.forwardChanged(v)
		s.ls[v] = s.lf[v] - dur
		changedLF = s.backwardChanged(v)
		s.updatePF(mergeInts([]int{v}, changedES))
		s.refreshTFs(mergeInts([]int{v}, changedES, changedLF))
	}
	added, removed := s.refreshCritical(oldCrit)
	return s.report(changedES, changedLF, oldPF, added, removed), nil
}

func (s *Scheduler) SetConstraint(v int, earliestStart, latestFinish int64) (*UpdateReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if earliestStart < 0 || earliestStart > 1_000_000_000 ||
		latestFinish < -1 || latestFinish > 1_000_000_000_000 {
		return nil, fmt.Errorf("%w: constraint is out of range", ErrInvalidArgument)
	}
	if !s.taskExists(v) {
		return nil, ErrTaskNotFound
	}

	oldCrit := s.snapshotCritical()
	oldPF := s.currentPF()
	s.fwdEval, s.bwdEval = 0, 0
	var changedES, changedLF []int
	if s.snet[v] != earliestStart {
		s.snet[v] = earliestStart
		changedES = s.forwardSeeds([]int{v})
		s.updatePF(mergeInts([]int{v}, changedES))
	}
	if s.fnlt[v] != latestFinish {
		s.fnlt[v] = latestFinish
		changedLF = s.backwardSeeds([]int{v})
	}
	s.refreshTFs(mergeInts([]int{v}, changedES, changedLF))
	added, removed := s.refreshCritical(oldCrit)
	return s.report(changedES, changedLF, oldPF, added, removed), nil
}

func (s *Scheduler) RemoveDep(u, v int) (*UpdateReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.taskExists(u) || !s.taskExists(v) {
		return nil, ErrTaskNotFound
	}
	if !s.edgeExists(u, v) {
		return nil, ErrDepNotFound
	}

	oldCrit := s.snapshotCritical()
	oldPF := s.currentPF()
	s.fwdEval, s.bwdEval = 0, 0
	s.deleteEdge(u, v)
	s.edgeCount--

	changedES := s.forwardSeeds([]int{v})
	changedLF := s.backwardSeeds([]int{u})
	s.updatePF(mergeInts([]int{u, v}, changedES))
	s.refreshTFs(mergeInts([]int{u, v}, changedES, changedLF))
	added, removed := s.refreshCritical(oldCrit)
	return s.report(changedES, changedLF, oldPF, added, removed), nil
}

func (s *Scheduler) report(changedES, changedLF []int, oldPF int64, added, removed []int) *UpdateReport {
	return &UpdateReport{
		ChangedES:   nonNilSorted(changedES),
		ChangedLF:   nonNilSorted(changedLF),
		OldPF:       oldPF,
		NewPF:       s.currentPF(),
		CritAdded:   nonNilSorted(added),
		CritRemoved: nonNilSorted(removed),
	}
}
