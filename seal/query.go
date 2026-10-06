package seal

func (s *Service) GetApplication(applicationID string) (Application, bool) {
	if applicationID == "" {
		return Application{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	application, exists := s.applications[applicationID]
	if !exists {
		return Application{}, false
	}
	return cloneApplication(application), true
}

func (s *Service) IsFrozen(employeeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frozen[employeeID]
}

func (s *Service) DailyUsage(authorizationID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dailyCount[authorizationID]
}

func (s *Service) DailyUsageAt(authorizationID string, now int64) int64 {
	if now < 0 || authorizationID == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dailyDay[authorizationID] != now/86400 {
		return 0
	}
	return s.dailyCount[authorizationID]
}

func cloneApplication(application *Application) Application {
	copyValue := *application
	copyValue.Approvals = make(map[string]bool, len(application.Approvals))
	for approverID, approved := range application.Approvals {
		copyValue.Approvals[approverID] = approved
	}
	copyValue.Confirmers = make(map[string]struct{}, len(application.Confirmers))
	for custodianID := range application.Confirmers {
		copyValue.Confirmers[custodianID] = struct{}{}
	}
	return copyValue
}
