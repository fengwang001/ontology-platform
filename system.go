package crew

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

type person struct {
	id             string
	duties         map[string]*storedDuty
	index          *bucketIndex
	qualifications map[string]int
}

type System struct {
	mu     sync.Mutex
	config Config
	people map[string]*person
	clock  int
	log    io.Writer
}

func NewSystem(config Config) *System {
	if err := config.Validate(); err != nil {
		panic(err)
	}
	return &System{
		config: config,
		people: make(map[string]*person),
		log:    io.Discard,
	}
}

func (s *System) SetLogger(writer io.Writer) {
	if writer == nil {
		writer = io.Discard
	}
	s.log = writer
}

func (s *System) AddPerson(now int, id string) *Rejection {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || id == "" {
		return s.recordReject(now, id, "add_person", nil, &Rejection{Code: InvalidArgument})
	}
	if rejection := s.clockCheck(now); rejection != nil {
		return s.recordReject(now, id, "add_person", nil, rejection)
	}
	if _, exists := s.people[id]; exists {
		return s.reject(now, id, "add_person", nil, InvalidArgument)
	}
	s.people[id] = &person{
		id:             id,
		duties:         make(map[string]*storedDuty),
		index:          newBucketIndex(s.config.maxLookback() + 1),
		qualifications: make(map[string]int),
	}
	return s.accept(now, id, "add_person", nil)
}

func (s *System) SetQualification(now int, personID, qualification string, expiry int) *Rejection {
	s.mu.Lock()
	defer s.mu.Unlock()

	if qualification == "" || expiry < 0 {
		return s.recordReject(now, personID, "set_qualification", nil, &Rejection{Code: InvalidArgument})
	}
	target := &storedDuty{DutyPeriod: DutyPeriod{Qualification: qualification}}
	if rejection := s.beginChange(now, personID, "set_qualification", target); rejection != nil {
		return rejection
	}
	s.people[personID].qualifications[qualification] = expiry
	return s.accept(now, personID, "set_qualification", target)
}

func (s *System) RevokeQualification(now int, personID, qualification string) *Rejection {
	s.mu.Lock()
	defer s.mu.Unlock()

	if qualification == "" {
		return s.recordReject(now, personID, "revoke_qualification", nil, &Rejection{Code: InvalidArgument})
	}
	target := &storedDuty{DutyPeriod: DutyPeriod{Qualification: qualification}}
	if rejection := s.beginChange(now, personID, "revoke_qualification", target); rejection != nil {
		return rejection
	}
	delete(s.people[personID].qualifications, qualification)
	return s.accept(now, personID, "revoke_qualification", target)
}

func (s *System) Register(now int, duty DutyPeriod) *Rejection {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rejection := invalidDuty(duty); rejection != nil {
		return s.recordReject(now, duty.PersonID, "register", nil, rejection)
	}
	target := &storedDuty{DutyPeriod: duty}
	if rejection := s.beginChange(now, duty.PersonID, "register", target); rejection != nil {
		return rejection
	}
	crew := s.people[duty.PersonID]
	if _, exists := crew.duties[duty.ID]; exists {
		return s.recordReject(now, crew.id, "register", target, &Rejection{Code: InvalidArgument})
	}
	if rejection := s.checkDuty(crew, target, false, nil); rejection != nil {
		return s.recordReject(now, crew.id, "register", target, rejection)
	}
	crew.duties[duty.ID] = target
	crew.index.add(target)
	return s.accept(now, crew.id, "register", target)
}

func (s *System) Extend(now int, personID, dutyID string, newEnd int) *Rejection {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || personID == "" || dutyID == "" || newEnd < 0 {
		return s.recordReject(now, personID, "extend", nil, &Rejection{Code: InvalidArgument})
	}
	if rejection := s.clockCheck(now); rejection != nil {
		return s.recordReject(now, personID, "extend", nil, rejection)
	}
	crew, exists := s.people[personID]
	if !exists {
		return s.reject(now, personID, "extend", nil, PersonNotFound)
	}
	original, exists := crew.duties[dutyID]
	if !exists {
		return s.reject(now, personID, "extend", nil, DutyNotFound)
	}
	if original.End <= now {
		return s.reject(now, personID, "extend", original, AlreadyStartedOrReleased)
	}
	if newEnd <= original.End || newEnd > original.End+s.config.MaximumExtension {
		return s.recordReject(now, personID, "extend", original, &Rejection{Code: InvalidArgument})
	}
	if original.extended {
		return s.recordReject(now, personID, "extend", original, &Rejection{Code: ExtensionRuleViolated})
	}
	candidate := *original
	candidate.End = newEnd
	candidate.extended = true
	crew.index.remove(original)
	rejection := s.checkDuty(crew, &candidate, true, original)
	if rejection != nil {
		crew.index.add(original)
		return s.recordReject(now, personID, "extend", &candidate, rejection)
	}
	crew.duties[dutyID] = &candidate
	crew.index.add(&candidate)
	return s.accept(now, personID, "extend", &candidate)
}

func (s *System) Revoke(now int, personID, dutyID string) *Rejection {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 || personID == "" || dutyID == "" {
		return s.recordReject(now, personID, "revoke", nil, &Rejection{Code: InvalidArgument})
	}
	if rejection := s.clockCheck(now); rejection != nil {
		return s.recordReject(now, personID, "revoke", nil, rejection)
	}
	crew, exists := s.people[personID]
	if !exists {
		return s.reject(now, personID, "revoke", nil, PersonNotFound)
	}
	duty, exists := crew.duties[dutyID]
	if !exists {
		return s.reject(now, personID, "revoke", nil, DutyNotFound)
	}
	if duty.Start <= now || duty.End <= now {
		return s.reject(now, personID, "revoke", duty, AlreadyStartedOrReleased)
	}
	delete(crew.duties, duty.ID)
	crew.index.remove(duty)
	return s.accept(now, personID, "revoke", duty)
}

func (s *System) beginChange(now int, personID, operation string, target *storedDuty) *Rejection {
	if now < 0 || personID == "" {
		return s.recordReject(now, personID, operation, target, &Rejection{Code: InvalidArgument})
	}
	if rejection := s.clockCheck(now); rejection != nil {
		return s.recordReject(now, personID, operation, target, rejection)
	}
	if _, exists := s.people[personID]; !exists {
		return s.recordReject(now, personID, operation, target, &Rejection{Code: PersonNotFound})
	}
	return nil
}

func (s *System) clockCheck(now int) *Rejection {
	if now < s.clock {
		return &Rejection{Code: ClockRollback}
	}
	return nil
}

func (s *System) accept(now int, personID, operation string, target *storedDuty) *Rejection {
	s.clock = now
	s.logf(now, personID, operation, target, "accepted")
	return nil
}

func (s *System) reject(now int, personID, operation string, target *storedDuty, code RejectionCode) *Rejection {
	rejection := &Rejection{Code: code}
	return s.recordReject(now, personID, operation, target, rejection)
}

func (s *System) recordReject(now int, personID, operation string, target *storedDuty, rejection *Rejection) *Rejection {
	reason := rejection.Error()
	if rejection.WindowStart != 0 {
		reason = fmt.Sprintf("%s windowStart=%d", reason, rejection.WindowStart)
	}
	s.logf(now, personID, operation, target, "rejected: "+reason)
	return rejection
}

func (s *System) logf(now int, personID, operation string, target *storedDuty, result string) {
	fields := make([]string, 0, 6)
	fields = append(fields, fmt.Sprintf("now=%d", now), "person="+personID, "op="+operation)
	if target != nil {
		fields = append(fields, fmt.Sprintf("duty={id:%q start:%d end:%d segments:%d qualification:%q extended:%t}",
			target.ID, target.Start, target.End, target.Segments, target.Qualification, target.extended))
	}
	fmt.Fprintln(s.log, strings.Join(fields, " ")+" => "+result)
}

func invalidDuty(duty DutyPeriod) *Rejection {
	if duty.PersonID == "" || duty.ID == "" || duty.Start < 0 || duty.End <= duty.Start ||
		duty.Segments < 0 || duty.Segments > 8 {
		return &Rejection{Code: InvalidArgument}
	}
	return nil
}
