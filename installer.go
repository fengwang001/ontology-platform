package ontology

import (
	"errors"
	"sort"
	"sync"
)

type Message struct {
	Sender string
	Seq    int
}

type Plan struct {
	ViewID   int
	Members  []string
	CatchUps map[string][]Message
	Agreed   map[string]int
}

var (
	ErrNotInChange     = errors.New("not in a view change")
	ErrAlreadyInChange = errors.New("already in a view change")
	ErrEmptyMembers    = errors.New("new members must not be empty")
	ErrDuplicateMember = errors.New("new members must be unique")
	ErrEmptyMemberID   = errors.New("member id must not be empty")
	ErrSameMembers     = errors.New("new members are identical to current members")
	ErrNotOldMajority  = errors.New("survivors do not form an old-view majority")
	ErrNotSurvivor     = errors.New("member is not a survivor")
	ErrDuplicateReport = errors.New("member already reported")
	ErrUnknownSender   = errors.New("counts contain a sender that is not an old member")
	ErrReportPending   = errors.New("some survivors have not reported")
)

type Installer struct {
	mu sync.Mutex

	viewID     int
	members    []string
	inChange   bool
	survivors  []string
	newMembers []string
	reports    map[string]map[string]int
}

func NewInstaller(members []string) *Installer {
	return &Installer{
		viewID:  1,
		members: cloneAndSortStrings(members),
	}
}

func (i *Installer) BeginChange(newMembers []string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.inChange {
		return ErrAlreadyInChange
	}
	if len(newMembers) == 0 {
		return ErrEmptyMembers
	}

	seen := make(map[string]struct{}, len(newMembers))
	for _, member := range newMembers {
		if _, exists := seen[member]; exists {
			return ErrDuplicateMember
		}
		seen[member] = struct{}{}
	}
	for _, member := range newMembers {
		if member == "" {
			return ErrEmptyMemberID
		}
	}

	oldSet := stringSliceSet(i.members)
	if len(seen) == len(oldSet) {
		same := true
		for member := range seen {
			if _, exists := oldSet[member]; !exists {
				same = false
				break
			}
		}
		if same {
			return ErrSameMembers
		}
	}

	survivors := make([]string, 0, len(i.members))
	for _, member := range i.members {
		if _, exists := seen[member]; exists {
			survivors = append(survivors, member)
		}
	}
	if len(survivors)*2 <= len(i.members) {
		return ErrNotOldMajority
	}

	i.inChange = true
	i.survivors = append([]string(nil), survivors...)
	i.newMembers = cloneAndSortStrings(newMembers)
	i.reports = make(map[string]map[string]int, len(survivors))
	return nil
}

func (i *Installer) Report(member string, counts map[string]int) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if !i.inChange {
		return ErrNotInChange
	}

	survivorSet := stringSliceSet(i.survivors)
	if _, exists := survivorSet[member]; !exists {
		return ErrNotSurvivor
	}
	if _, exists := i.reports[member]; exists {
		return ErrDuplicateReport
	}

	oldSet := stringSliceSet(i.members)
	for sender := range counts {
		if _, exists := oldSet[sender]; !exists {
			return ErrUnknownSender
		}
	}

	report := make(map[string]int, len(i.members))
	for _, sender := range i.members {
		report[sender] = counts[sender]
	}
	i.reports[member] = report
	return nil
}

func (i *Installer) Complete() (*Plan, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if !i.inChange {
		return nil, ErrNotInChange
	}
	if len(i.reports) != len(i.survivors) {
		return nil, ErrReportPending
	}

	agreed := make(map[string]int, len(i.members))
	for _, sender := range i.members {
		maxSeq := 0
		for _, survivor := range i.survivors {
			if seq := i.reports[survivor][sender]; seq > maxSeq {
				maxSeq = seq
			}
		}
		agreed[sender] = maxSeq
	}

	catchUps := make(map[string][]Message, len(i.survivors))
	for _, survivor := range i.survivors {
		messages := make([]Message, 0)
		for _, sender := range i.members {
			for seq := i.reports[survivor][sender] + 1; seq <= agreed[sender]; seq++ {
				messages = append(messages, Message{Sender: sender, Seq: seq})
			}
		}
		catchUps[survivor] = messages
	}

	plan := &Plan{
		ViewID:   i.viewID + 1,
		Members:  append([]string(nil), i.newMembers...),
		CatchUps: catchUps,
		Agreed:   cloneCounts(agreed),
	}

	i.viewID++
	i.members = append([]string(nil), i.newMembers...)
	i.inChange = false
	i.survivors = nil
	i.newMembers = nil
	i.reports = nil

	return plan, nil
}

func (i *Installer) Abort() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if !i.inChange {
		return ErrNotInChange
	}

	i.inChange = false
	i.survivors = nil
	i.newMembers = nil
	i.reports = nil
	return nil
}

func cloneAndSortStrings(values []string) []string {
	cloned := append([]string(nil), values...)
	sort.Strings(cloned)
	return cloned
}

func stringSliceSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func cloneCounts(counts map[string]int) map[string]int {
	cloned := make(map[string]int, len(counts))
	for sender, seq := range counts {
		cloned[sender] = seq
	}
	return cloned
}
