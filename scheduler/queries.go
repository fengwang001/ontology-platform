package scheduler

func (s *Scheduler) timeValue(v int, value func(int) int64) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.taskExistsLocked(v) {
		return 0, ErrTaskNotFound
	}
	return value(v), nil
}

func (s *Scheduler) ES(v int) (int64, error) {
	return s.timeValue(v, func(x int) int64 { return s.es[x] })
}

func (s *Scheduler) EF(v int) (int64, error) {
	return s.timeValue(v, func(x int) int64 { return s.ef[x] })
}

func (s *Scheduler) LS(v int) (int64, error) {
	return s.timeValue(v, func(x int) int64 { return s.ls[x] })
}

func (s *Scheduler) LF(v int) (int64, error) {
	return s.timeValue(v, func(x int) int64 { return s.lf[x] })
}

func (s *Scheduler) TF(v int) (int64, error) {
	return s.timeValue(v, func(x int) int64 { return s.tf[x] })
}

func (s *Scheduler) FF(v int) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.taskExistsLocked(v) {
		return 0, ErrTaskNotFound
	}
	if len(s.succs[v]) == 0 {
		return s.currentPF() - s.ef[v], nil
	}
	var ff int64
	for i, w := range s.succs[v] {
		lag, _ := s.edgeValue(v, w)
		value := s.es[w] - lag - s.ef[v]
		if i == 0 || value < ff {
			ff = value
		}
	}
	return ff, nil
}

func (s *Scheduler) PF() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentPF()
}

func (s *Scheduler) SetBaseline() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baselineEF = append(s.baselineEF[:0], s.ef...)
	s.baselineSet = true
}

func (s *Scheduler) Variance(v int) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.taskExistsLocked(v) {
		return 0, ErrTaskNotFound
	}
	if !s.baselineSet || v >= len(s.baselineEF) {
		return 0, ErrNoBaseline
	}
	return s.ef[v] - s.baselineEF[v], nil
}

func (s *Scheduler) FwdEval() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fwdEval
}

func (s *Scheduler) BwdEval() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bwdEval
}
