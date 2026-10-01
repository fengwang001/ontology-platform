package membership

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrAlreadyChanging    = errors.New("membership: change already in progress")
	ErrEmptyMembers       = errors.New("membership: member set is empty")
	ErrDuplicateMember    = errors.New("membership: member set contains duplicates")
	ErrEmptyMemberID      = errors.New("membership: member identifier is empty")
	ErrSameMembership     = errors.New("membership: new membership is unchanged")
	ErrInsufficientQuorum = errors.New("membership: survivors do not form an old-view majority")
	ErrNotChanging        = errors.New("membership: no change in progress")
	ErrNotSurvivor        = errors.New("membership: reporter is not a surviving member")
	ErrDuplicateReport    = errors.New("membership: member already reported")
	ErrUnknownSender      = errors.New("membership: count contains sender outside old membership")
	ErrIncompleteReports  = errors.New("membership: not all survivors have reported")
)

type Message struct {
	Sender   string
	Sequence uint64
}

type Plan struct {
	ViewID     uint64
	NewMembers []string
	CatchUp    map[string][]Message
	Agreed     map[string]uint64
}

type Installer struct {
	mu         sync.Mutex
	viewID     uint64
	members    map[string]struct{}
	survivors  map[string]struct{}
	newMembers map[string]struct{}
	reports    map[string]map[string]uint64
	inChange   bool
}

func NewInstaller(initialMembers []string) (*Installer, error) {
	members, err := validateMemberSet(initialMembers)
	if err != nil {
		return nil, err
	}
	return &Installer{
		viewID:  1,
		members: members,
	}, nil
}

func (i *Installer) BeginChange(memberIDs []string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.inChange {
		return ErrAlreadyChanging
	}
	nextMembers, err := validateMemberSet(memberIDs)
	if err != nil {
		return err
	}
	if sameMemberSet(i.members, nextMembers) {
		return ErrSameMembership
	}

	survivors := make(map[string]struct{})
	for member := range nextMembers {
		if _, ok := i.members[member]; ok {
			survivors[member] = struct{}{}
		}
	}
	if len(survivors)*2 <= len(i.members) {
		return ErrInsufficientQuorum
	}

	i.survivors = survivors
	i.newMembers = nextMembers
	i.reports = make(map[string]map[string]uint64, len(survivors))
	i.inChange = true
	return nil
}

func (i *Installer) Report(member string, counts map[string]uint64) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if !i.inChange {
		return ErrNotChanging
	}
	if _, ok := i.survivors[member]; !ok {
		return ErrNotSurvivor
	}
	if _, ok := i.reports[member]; ok {
		return ErrDuplicateReport
	}

	normalizedCounts := make(map[string]uint64, len(i.members))
	for sender, sequence := range counts {
		if _, ok := i.members[sender]; !ok {
			return ErrUnknownSender
		}
		normalizedCounts[sender] = sequence
	}
	i.reports[member] = normalizedCounts
	return nil
}

func (i *Installer) Complete() (*Plan, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if !i.inChange {
		return nil, ErrNotChanging
	}
	if len(i.reports) != len(i.survivors) {
		return nil, ErrIncompleteReports
	}

	senders := sortedMembers(i.members)
	agreed := make(map[string]uint64, len(senders))
	for _, sender := range senders {
		var maximum uint64
		for _, counts := range i.reports {
			if sequence := counts[sender]; sequence > maximum {
				maximum = sequence
			}
		}
		agreed[sender] = maximum
	}

	catchUp := make(map[string][]Message, len(i.survivors))
	for survivor := range i.survivors {
		messages := make([]Message, 0)
		counts := i.reports[survivor]
		for _, sender := range senders {
			delivered := counts[sender]
			for sequence := delivered; sequence < agreed[sender]; sequence++ {
				messages = append(messages, Message{Sender: sender, Sequence: sequence + 1})
			}
		}
		catchUp[survivor] = messages
	}

	plan := &Plan{
		ViewID:     i.viewID + 1,
		NewMembers: sortedMembers(i.newMembers),
		CatchUp:    catchUp,
		Agreed:     agreed,
	}

	i.viewID++
	i.members = i.newMembers
	i.survivors = nil
	i.newMembers = nil
	i.reports = nil
	i.inChange = false
	return clonePlan(plan), nil
}

func (i *Installer) Abort() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if !i.inChange {
		return ErrNotChanging
	}

	i.survivors = nil
	i.newMembers = nil
	i.reports = nil
	i.inChange = false
	return nil
}

func validateMemberSet(memberIDs []string) (map[string]struct{}, error) {
	if len(memberIDs) == 0 {
		return nil, ErrEmptyMembers
	}
	members := make(map[string]struct{}, len(memberIDs))
	seen := make(map[string]struct{}, len(memberIDs))
	for _, member := range memberIDs {
		if _, ok := seen[member]; ok {
			return nil, ErrDuplicateMember
		}
		seen[member] = struct{}{}
	}
	for _, member := range memberIDs {
		if member == "" {
			return nil, ErrEmptyMemberID
		}
		members[member] = struct{}{}
	}
	return members, nil
}

func sameMemberSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for member := range left {
		if _, ok := right[member]; !ok {
			return false
		}
	}
	return true
}

func sortedMembers(members map[string]struct{}) []string {
	result := make([]string, 0, len(members))
	for member := range members {
		result = append(result, member)
	}
	sort.Strings(result)
	return result
}

func clonePlan(plan *Plan) *Plan {
	cloned := &Plan{
		ViewID:     plan.ViewID,
		NewMembers: append([]string(nil), plan.NewMembers...),
		CatchUp:    make(map[string][]Message, len(plan.CatchUp)),
		Agreed:     make(map[string]uint64, len(plan.Agreed)),
	}
	for member, messages := range plan.CatchUp {
		clonedMessages := make([]Message, len(messages))
		copy(clonedMessages, messages)
		cloned.CatchUp[member] = clonedMessages
	}
	for sender, sequence := range plan.Agreed {
		cloned.Agreed[sender] = sequence
	}
	return cloned
}
