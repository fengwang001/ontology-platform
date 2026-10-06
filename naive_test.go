package crew

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type naiveSystem struct {
	config Config
	clock  int
	people map[string]*naivePerson
	log    strings.Builder
}

type naivePerson struct {
	duties         map[string]*storedDuty
	ordered        []*storedDuty
	qualifications map[string]int
}

func newNaiveSystem(config Config) *naiveSystem {
	return &naiveSystem{config: config, people: map[string]*naivePerson{}}
}

func (n *naiveSystem) addPerson(now int, id string) *Rejection {
	if now < 0 || id == "" {
		return n.reject(now, id, "add_person", nil, InvalidArgument)
	}
	if now < n.clock {
		return n.reject(now, id, "add_person", nil, ClockRollback)
	}
	if _, ok := n.people[id]; ok {
		return n.reject(now, id, "add_person", nil, InvalidArgument)
	}
	n.people[id] = &naivePerson{duties: map[string]*storedDuty{}, qualifications: map[string]int{}}
	n.accept(now, id, "add_person", nil)
	return nil
}

func (n *naiveSystem) setQualification(now int, personID, qualification string, expiry int) *Rejection {
	if now < 0 || personID == "" || qualification == "" || expiry < 0 {
		return n.reject(now, personID, "set_qualification", nil, InvalidArgument)
	}
	if r := n.checkClock(now, personID, "set_qualification", nil); r != nil {
		return r
	}
	n.people[personID].qualifications[qualification] = expiry
	n.accept(now, personID, "set_qualification", nil)
	return nil
}

func (n *naiveSystem) revokeQualification(now int, personID, qualification string) *Rejection {
	if now < 0 || personID == "" || qualification == "" {
		return n.reject(now, personID, "revoke_qualification", nil, InvalidArgument)
	}
	if r := n.checkClock(now, personID, "revoke_qualification", nil); r != nil {
		return r
	}
	delete(n.people[personID].qualifications, qualification)
	n.accept(now, personID, "revoke_qualification", nil)
	return nil
}

func (n *naiveSystem) register(now int, duty DutyPeriod) *Rejection {
	if duty.PersonID == "" || duty.ID == "" || duty.Start < 0 || duty.End <= duty.Start || duty.Segments < 0 || duty.Segments > 8 {
		return n.reject(now, duty.PersonID, "register", nil, InvalidArgument)
	}
	if r := n.checkClock(now, duty.PersonID, "register", nil); r != nil {
		return r
	}
	crew := n.people[duty.PersonID]
	if _, ok := crew.duties[duty.ID]; ok {
		return n.reject(now, duty.PersonID, "register", nil, InvalidArgument)
	}
	candidate := &storedDuty{DutyPeriod: duty}
	if r := n.check(crew, candidate, false, nil); r != nil {
		return n.reject(now, duty.PersonID, "register", candidate, r.Code)
	}
	crew.duties[duty.ID] = candidate
	crew.ordered = append(crew.ordered, candidate)
	sort.Slice(crew.ordered, func(i, j int) bool { return crew.ordered[i].Start < crew.ordered[j].Start })
	n.accept(now, duty.PersonID, "register", candidate)
	return nil
}

func (n *naiveSystem) extend(now int, personID, dutyID string, newEnd int) *Rejection {
	if now < 0 || personID == "" || dutyID == "" || newEnd < 0 {
		return n.reject(now, personID, "extend", nil, InvalidArgument)
	}
	if r := n.checkClock(now, personID, "extend", nil); r != nil {
		return r
	}
	crew := n.people[personID]
	original := crew.duties[dutyID]
	if original == nil {
		return n.reject(now, personID, "extend", nil, DutyNotFound)
	}
	if original.End <= now {
		return n.reject(now, personID, "extend", original, AlreadyStartedOrReleased)
	}
	if newEnd <= original.End || newEnd > original.End+n.config.MaximumExtension {
		return n.reject(now, personID, "extend", original, InvalidArgument)
	}
	if original.extended {
		return n.reject(now, personID, "extend", original, ExtensionRuleViolated)
	}
	candidate := *original
	candidate.End = newEnd
	candidate.extended = true
	if r := n.check(crew, &candidate, true, original); r != nil {
		return n.reject(now, personID, "extend", &candidate, r.Code)
	}
	*original = candidate
	n.accept(now, personID, "extend", original)
	return nil
}

func (n *naiveSystem) revoke(now int, personID, dutyID string) *Rejection {
	if now < 0 || personID == "" || dutyID == "" {
		return n.reject(now, personID, "revoke", nil, InvalidArgument)
	}
	if r := n.checkClock(now, personID, "revoke", nil); r != nil {
		return r
	}
	crew := n.people[personID]
	duty := crew.duties[dutyID]
	if duty == nil {
		return n.reject(now, personID, "revoke", nil, DutyNotFound)
	}
	if duty.Start <= now || duty.End <= now {
		return n.reject(now, personID, "revoke", duty, AlreadyStartedOrReleased)
	}
	delete(crew.duties, dutyID)
	for i, existing := range crew.ordered {
		if existing.ID == dutyID {
			crew.ordered = append(crew.ordered[:i], crew.ordered[i+1:]...)
			break
		}
	}
	n.accept(now, personID, "revoke", duty)
	return nil
}

func (n *naiveSystem) checkClock(now int, personID, operation string, duty *storedDuty) *Rejection {
	if now < n.clock {
		return n.reject(now, personID, operation, duty, ClockRollback)
	}
	if _, ok := n.people[personID]; !ok {
		return n.reject(now, personID, operation, duty, PersonNotFound)
	}
	return nil
}

func (n *naiveSystem) check(crew *naivePerson, candidate *storedDuty, isExtension bool, excluded *storedDuty) *Rejection {
	if candidate.Qualification != "" {
		expiry, ok := crew.qualifications[candidate.Qualification]
		if !ok || expiry <= candidate.End {
			return &Rejection{Code: QualificationInvalid}
		}
	}
	var previous, next *storedDuty
	for _, existing := range crew.ordered {
		if excluded != nil && existing.ID == excluded.ID {
			continue
		}
		if existing.Start <= candidate.End && candidate.Start <= existing.End {
			return &Rejection{Code: OverlappingDuty}
		}
		if existing.Start < candidate.Start && (previous == nil || existing.Start > previous.Start) {
			previous = existing
		}
		if existing.Start > candidate.Start && (next == nil || existing.Start < next.Start) {
			next = existing
		}
	}
	if previous != nil {
		required := maxInt(n.config.MinimumRest, durationOf(previous))
		if candidate.Start-previous.End < required {
			return &Rejection{Code: InsufficientRest}
		}
	}
	if next != nil {
		required := maxInt(n.config.MinimumRest, durationOf(candidate))
		if next.Start-candidate.End < required {
			return &Rejection{Code: InsufficientRest}
		}
	}
	if durationOf(candidate) > n.config.dutyLimit(candidate.Start, candidate.Segments, isExtension) {
		return &Rejection{Code: DutyLimitExceeded}
	}
	if isExtension {
		for _, existing := range crew.ordered {
			if excluded != nil && existing.ID == excluded.ID {
				continue
			}
			if existing.extended && absInt(existing.Start-candidate.Start) < SevenDays {
				return &Rejection{Code: ExtensionRuleViolated}
			}
		}
	}
	if window, exceeded := n.cumulative(crew, candidate, excluded, SevenDays, n.config.SevenDayLimit); exceeded {
		return &Rejection{Code: SevenDayLimitExceeded, WindowStart: window}
	}
	if window, exceeded := n.cumulative(crew, candidate, excluded, TwentyEightDays, n.config.TwentyEightDayLimit); exceeded {
		return &Rejection{Code: TwentyEightDayLimitExceeded, WindowStart: window}
	}
	return nil
}

func (n *naiveSystem) cumulative(crew *naivePerson, candidate *storedDuty, excluded *storedDuty, width, limit int) (int, bool) {
	for windowStart := 0; windowStart <= candidate.End+width; windowStart++ {
		total := overlap(candidate.Start, candidate.End, windowStart, windowStart+width)
		for _, duty := range crew.ordered {
			if excluded != nil && duty.ID == excluded.ID {
				continue
			}
			total += overlap(duty.Start, duty.End, windowStart, windowStart+width)
		}
		if total > limit {
			return windowStart, true
		}
	}
	return 0, false
}

func (n *naiveSystem) nextReport(now int, personID string, segments int, qualification string, horizon int) (int, bool) {
	if now < 0 || personID == "" || segments < 0 || segments > 8 || horizon < now {
		return 0, false
	}
	crew := n.people[personID]
	for start := now; start <= horizon; start++ {
		candidate := &storedDuty{DutyPeriod: DutyPeriod{
			PersonID:      personID,
			Start:         start,
			End:           start + 1,
			Segments:      segments,
			Qualification: qualification,
		}}
		if n.check(crew, candidate, false, nil) == nil {
			return start, true
		}
	}
	return 0, false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (n *naiveSystem) accept(now int, personID, operation string, duty *storedDuty) {
	n.clock = now
	n.record(now, personID, operation, duty, "accepted")
}

func (n *naiveSystem) reject(now int, personID, operation string, duty *storedDuty, code RejectionCode) *Rejection {
	r := &Rejection{Code: code}
	n.record(now, personID, operation, duty, r.Error())
	return r
}

func (n *naiveSystem) record(now int, personID, operation string, duty *storedDuty, result string) {
	line := fmt.Sprintf("now=%d person=%s op=%s", now, personID, operation)
	if duty != nil {
		line += fmt.Sprintf(" duty=%q[%d,%d] segments=%d qual=%q extended=%t", duty.ID, duty.Start, duty.End, duty.Segments, duty.Qualification, duty.extended)
	}
	n.log.WriteString(line + " => " + result + "\n")
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	random := rand.New(rand.NewSource(1452))
	for iteration := 0; iteration < 80; iteration++ {
		config := testConfig()
		config.MinimumRest = 20 + random.Intn(90)
		config.SevenDayLimit = 600 + random.Intn(1800)
		config.TwentyEightDayLimit = 3000 + random.Intn(8000)
		config.MaximumExtension = 30 + random.Intn(120)
		actual := NewSystem(config)
		reference := newNaiveSystem(config)
		var log strings.Builder
		actual.SetLogger(&log)
		_ = actual.AddPerson(0, "p")
		_ = reference.addPerson(0, "p")
		activeDuties := make([]string, 0)

		for step := 0; step < 35; step++ {
			now := random.Intn(45 * DayLength)
			operation := random.Intn(6)
			var got, want *Rejection
			switch operation {
			case 0:
				qualification := string(rune('A' + random.Intn(3)))
				expiry := random.Intn(45 * DayLength)
				got = actual.SetQualification(now, "p", qualification, expiry)
				want = reference.setQualification(now, "p", qualification, expiry)
			case 1:
				id := fmt.Sprintf("i%d-s%d", iteration, step)
				start := random.Intn(40 * DayLength)
				duty := DutyPeriod{
					PersonID:      "p",
					ID:            id,
					Start:         start,
					End:           start + 30 + random.Intn(750),
					Segments:      random.Intn(9),
					Qualification: "",
				}
				if random.Intn(2) == 0 {
					duty.Qualification = string(rune('A' + random.Intn(3)))
				}
				got = actual.Register(now, duty)
				want = reference.register(now, duty)
				if got == nil {
					activeDuties = append(activeDuties, id)
				}
			case 2:
				if len(activeDuties) == 0 {
					continue
				}
				index := random.Intn(len(activeDuties))
				id := activeDuties[index]
				original := reference.people["p"].duties[id]
				newEnd := original.End + 1 + random.Intn(config.MaximumExtension+30)
				got = actual.Extend(now, "p", id, newEnd)
				want = reference.extend(now, "p", id, newEnd)
			case 3:
				if len(activeDuties) == 0 {
					continue
				}
				index := random.Intn(len(activeDuties))
				id := activeDuties[index]
				got = actual.Revoke(now, "p", id)
				want = reference.revoke(now, "p", id)
				if got == nil {
					activeDuties = append(activeDuties[:index], activeDuties[index+1:]...)
				}
			case 4:
				qualification := string(rune('A' + random.Intn(3)))
				got = actual.RevokeQualification(now, "p", qualification)
				want = reference.revokeQualification(now, "p", qualification)
			default:
				duplicate := DutyPeriod{PersonID: "p", ID: "duplicate", Start: 0, End: 1, Segments: 9}
				got = actual.Register(now, duplicate)
				want = reference.register(now, duplicate)
			}
			if !sameRejection(got, want) {
				log.WriteString(reference.log.String())
				t.Fatalf("iteration=%d step=%d now=%d operation=%d got=%#v want=%#v\n%s",
					iteration, step, now, operation, got, want, log.String())
			}
		}
	}
}

func sameRejection(got, want *Rejection) bool {
	if (got == nil) != (want == nil) {
		return false
	}
	return got == nil || got.Code == want.Code && got.WindowStart == want.WindowStart
}
