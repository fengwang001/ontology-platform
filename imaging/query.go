package imaging

func CodeOf(err error) ErrorCode {
	if domainErr, ok := err.(*DomainError); ok {
		return domainErr.Code
	}
	return ""
}

func (s *System) Appointment(id string) (Appointment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	appt, exists := s.appointments[id]
	return appt, exists
}
