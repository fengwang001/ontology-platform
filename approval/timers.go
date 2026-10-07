package approval

func (s *Service) dueAt(stage *stageState) *int {
	if stage.startedAt == nil || stage.remainingLimit <= 0 {
		return nil
	}
	if stage.segmentStartAt == nil {
		return nil
	}
	start := *stage.segmentStartAt
	due := s.days.NthWorkdayFrom(s.days.NextWorkday(start), stage.remainingLimit)
	return &due
}

func (s *Service) elapsedWorkdays(stage *stageState, day int) int {
	if stage.startedAt == nil {
		return 0
	}
	if stage.correctionAt != nil {
		if stage.segmentStartAt == nil || *stage.correctionAt < *stage.segmentStartAt {
			return 0
		}
		return s.days.WorkdaysBetweenInclusive(s.days.NextWorkday(*stage.segmentStartAt), *stage.correctionAt)
	}
	if stage.segmentStartAt == nil {
		return 0
	}
	return s.days.WorkdaysBetweenInclusive(s.days.NextWorkday(*stage.segmentStartAt), day)
}

func (s *Service) remainingWorkdays(stage *stageState, day int) *int {
	if stage.status != StageProcessing && stage.status != StageCorrecting {
		return nil
	}
	if stage.status == StageCorrecting {
		remaining := stage.remainingLimit
		return &remaining
	}
	due := s.dueAt(stage)
	if due == nil {
		value := 0
		return &value
	}
	if day < *due {
		value := s.days.WorkdaysBetweenInclusive(day, *due)
		return &value
	}
	if s.days.IsWorkday(day) && day == *due {
		value := 1
		return &value
	}
	value := 0
	return &value
}

func (s *Service) correctionDeadline(stage *stageState, requestDay int) int {
	return s.days.NthWorkdayFrom(requestDay+1, stage.definition.CorrectionDays)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
