// Package matching implements a capacitated two-sided stable matcher using
// the applicant-proposing deferred acceptance algorithm, together with a
// blocking-pair verifier for arbitrary assignments.
package matching

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

// Rejection and query errors. Every failure reported by this package wraps
// exactly one of these sentinels, so callers can distinguish causes with
// errors.Is.
var (
	// ErrFrozen: the registry was frozen by a successful Run.
	ErrFrozen = errors.New("matching: registry is frozen")
	// ErrInvalidID: the entity id is smaller than 1.
	ErrInvalidID = errors.New("matching: id must be >= 1")
	// ErrInvalidCapacity: a program capacity is smaller than 1.
	ErrInvalidCapacity = errors.New("matching: capacity must be >= 1")
	// ErrDuplicateID: the id is already registered.
	ErrDuplicateID = errors.New("matching: id already registered")
	// ErrInvalidPreference: a preference list contains an id smaller than 1.
	ErrInvalidPreference = errors.New("matching: preference id must be >= 1")
	// ErrDuplicatePreference: a preference list contains a duplicate id.
	ErrDuplicatePreference = errors.New("matching: duplicate preference id")
	// ErrUnknownReference: a preference list references an unregistered id.
	ErrUnknownReference = errors.New("matching: preference references unknown id")
	// ErrNotRun: Result or Verify was called before a successful Run.
	ErrNotRun = errors.New("matching: Run has not completed")
	// ErrUnknownMember: a matching references an unknown applicant or program.
	ErrUnknownMember = errors.New("matching: matching references unknown applicant or program")
	// ErrNotMutuallyAcceptable: a matched pair is not mutually acceptable.
	ErrNotMutuallyAcceptable = errors.New("matching: pair is not mutually acceptable")
	// ErrOverCapacity: a program is assigned more applicants than its capacity.
	ErrOverCapacity = errors.New("matching: program over capacity")
)

// Pair is a blocking pair: an applicant and a program that would both
// strictly prefer each other over their current situation.
type Pair struct {
	Applicant int
	Program   int
}

type applicant struct {
	prefs []int
}

type program struct {
	capacity int
	prefs    []int
	rank     map[int]int // applicant id -> position in prefs (0 = most preferred)
}

// Matcher registers applicants and programs, runs deferred acceptance, and
// verifies matchings. All methods are safe for concurrent use; concurrent
// calls behave as if executed in some serial order.
type Matcher struct {
	mu         sync.Mutex
	applicants map[int]applicant
	programs   map[int]program
	frozen     bool
	ran        bool
	result     map[int]int
	proposals  int
}

// New returns an empty Matcher.
func New() *Matcher {
	return &Matcher{
		applicants: make(map[int]applicant),
		programs:   make(map[int]program),
	}
}

func validatePrefs(prefs []int) error {
	for _, id := range prefs {
		if id < 1 {
			return fmt.Errorf("%w: %d", ErrInvalidPreference, id)
		}
	}
	seen := make(map[int]struct{}, len(prefs))
	for _, id := range prefs {
		if _, ok := seen[id]; ok {
			return fmt.Errorf("%w: %d", ErrDuplicatePreference, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// AddApplicant registers an applicant with a strict preference list of
// program ids (most preferred first), listing only acceptable programs.
// A rejected call changes nothing.
func (m *Matcher) AddApplicant(id int, prefs []int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return ErrFrozen
	}
	if id < 1 {
		return fmt.Errorf("%w: %d", ErrInvalidID, id)
	}
	if _, ok := m.applicants[id]; ok {
		return fmt.Errorf("%w: applicant %d", ErrDuplicateID, id)
	}
	if err := validatePrefs(prefs); err != nil {
		return err
	}
	m.applicants[id] = applicant{prefs: slices.Clone(prefs)}
	return nil
}

// AddProgram registers a program with a capacity and a strict preference
// list of applicant ids, listing only acceptable applicants. A rejected
// call changes nothing.
func (m *Matcher) AddProgram(id int, capacity int, prefs []int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return ErrFrozen
	}
	if id < 1 {
		return fmt.Errorf("%w: %d", ErrInvalidID, id)
	}
	if capacity < 1 {
		return fmt.Errorf("%w: %d", ErrInvalidCapacity, capacity)
	}
	if _, ok := m.programs[id]; ok {
		return fmt.Errorf("%w: program %d", ErrDuplicateID, id)
	}
	if err := validatePrefs(prefs); err != nil {
		return err
	}
	rank := make(map[int]int, len(prefs))
	for i, a := range prefs {
		rank[a] = i
	}
	m.programs[id] = program{capacity: capacity, prefs: slices.Clone(prefs), rank: rank}
	return nil
}

// Run validates all preference references, freezes the registry, and
// computes the applicant-optimal stable matching. If any preference
// references an unregistered id, Run reports the first such reference
// (applicants by ascending id, then programs by ascending id, each in
// list order) and leaves the registry unfrozen and unchanged. Calling
// Run again after a successful Run returns the same result without
// recomputing.
func (m *Matcher) Run() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return nil
	}
	for _, a := range slices.Sorted(maps.Keys(m.applicants)) {
		for _, p := range m.applicants[a].prefs {
			if _, ok := m.programs[p]; !ok {
				return fmt.Errorf("%w: applicant %d lists program %d", ErrUnknownReference, a, p)
			}
		}
	}
	for _, p := range slices.Sorted(maps.Keys(m.programs)) {
		for _, a := range m.programs[p].prefs {
			if _, ok := m.applicants[a]; !ok {
				return fmt.Errorf("%w: program %d lists applicant %d", ErrUnknownReference, p, a)
			}
		}
	}
	m.frozen = true
	m.result, m.proposals = m.compute()
	m.ran = true
	return nil
}

// compute runs applicant-proposing deferred acceptance. The queue starts
// with all applicants in ascending id order; a rejected or evicted
// applicant rejoins at the tail.
func (m *Matcher) compute() (map[int]int, int) {
	queue := slices.Sorted(maps.Keys(m.applicants))
	next := make(map[int]int, len(queue))        // applicant -> next pref index to propose to
	held := make(map[int][]int, len(m.programs)) // program -> tentatively held applicants
	proposals := 0

	for len(queue) > 0 {
		a := queue[0]
		queue = queue[1:]
		prefs := m.applicants[a].prefs
		if next[a] >= len(prefs) {
			continue // list exhausted: stays unmatched
		}
		p := prefs[next[a]]
		next[a]++
		proposals++
		prog := m.programs[p]
		if _, ok := prog.rank[a]; !ok {
			queue = append(queue, a) // not mutually acceptable: rejected
			continue
		}
		held[p] = append(held[p], a)
		if len(held[p]) > prog.capacity {
			worst := held[p][0]
			for _, x := range held[p][1:] {
				if prog.rank[x] > prog.rank[worst] {
					worst = x
				}
			}
			held[p] = slices.DeleteFunc(held[p], func(x int) bool { return x == worst })
			queue = append(queue, worst)
		}
	}

	result := make(map[int]int, len(m.applicants))
	for a := range m.applicants {
		result[a] = 0
	}
	for p, holders := range held {
		for _, a := range holders {
			result[a] = p
		}
	}
	return result, proposals
}

// Result returns the matching computed by Run as a map from every
// applicant id to its program id (0 = unmatched), plus the total number
// of proposals made. It reports ErrNotRun before a successful Run.
func (m *Matcher) Result() (map[int]int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ran {
		return nil, 0, ErrNotRun
	}
	return maps.Clone(m.result), m.proposals, nil
}

// Verify checks a proposed matching (applicant -> program; missing keys or
// 0 mean unmatched) and returns its blocking pairs sorted by (applicant,
// program). It reports, in this order and only the first violation:
// unknown applicant or program in the matching, a pair that is not
// mutually acceptable (ascending applicant), or a program over capacity
// (ascending program).
func (m *Matcher) Verify(match map[int]int) ([]Pair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ran {
		return nil, ErrNotRun
	}
	for _, a := range slices.Sorted(maps.Keys(match)) {
		if _, ok := m.applicants[a]; !ok {
			return nil, fmt.Errorf("%w: applicant %d", ErrUnknownMember, a)
		}
		if p := match[a]; p != 0 {
			if _, ok := m.programs[p]; !ok {
				return nil, fmt.Errorf("%w: program %d", ErrUnknownMember, p)
			}
		}
	}
	for _, a := range slices.Sorted(maps.Keys(match)) {
		p := match[a]
		if p == 0 {
			continue
		}
		if !m.mutuallyAcceptable(a, p) {
			return nil, fmt.Errorf("%w: applicant %d with program %d", ErrNotMutuallyAcceptable, a, p)
		}
	}
	holders := make(map[int][]int, len(m.programs))
	for a, p := range match {
		if p != 0 {
			holders[p] = append(holders[p], a)
		}
	}
	for _, p := range slices.Sorted(maps.Keys(m.programs)) {
		if len(holders[p]) > m.programs[p].capacity {
			return nil, fmt.Errorf("%w: program %d holds %d > %d", ErrOverCapacity, p, len(holders[p]), m.programs[p].capacity)
		}
	}

	var pairs []Pair
	for _, a := range slices.Sorted(maps.Keys(m.applicants)) {
		current := match[a]
		rankOf := make(map[int]int, len(m.applicants[a].prefs))
		for i, p := range m.applicants[a].prefs {
			rankOf[p] = i
		}
		for _, p := range slices.Sorted(maps.Keys(m.programs)) {
			if !m.mutuallyAcceptable(a, p) {
				continue
			}
			if current != 0 {
				curRank, ok := rankOf[current]
				if !ok || rankOf[p] >= curRank {
					continue // a does not strictly prefer p
				}
			}
			prog := m.programs[p]
			if len(holders[p]) < prog.capacity {
				pairs = append(pairs, Pair{Applicant: a, Program: p})
				continue
			}
			worst := holders[p][0]
			for _, x := range holders[p][1:] {
				if prog.rank[x] > prog.rank[worst] {
					worst = x
				}
			}
			if prog.rank[a] < prog.rank[worst] {
				pairs = append(pairs, Pair{Applicant: a, Program: p})
			}
		}
	}
	if pairs == nil {
		pairs = []Pair{}
	}
	return pairs, nil
}

// mutuallyAcceptable reports whether a lists p and p lists a. It must only
// be called after Run has validated all references.
func (m *Matcher) mutuallyAcceptable(a, p int) bool {
	if !slices.Contains(m.applicants[a].prefs, p) {
		return false
	}
	_, ok := m.programs[p].rank[a]
	return ok
}
