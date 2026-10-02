// Package matcher implements a capacitated two-sided stable matcher using
// the applicant-proposing deferred acceptance algorithm, together with a
// blocking-pair verifier for arbitrary matchings.
package matcher

import (
	"errors"
	"sort"
	"sync"
)

// Rejection reasons returned by AddApplicant and AddProgram. They are
// distinct sentinel values so callers can tell them apart with errors.Is.
var (
	ErrFrozen              = errors.New("matcher: registry is frozen")
	ErrInvalidID           = errors.New("matcher: id must be a positive integer")
	ErrInvalidCapacity     = errors.New("matcher: capacity must be a positive integer")
	ErrDuplicateID         = errors.New("matcher: id already registered")
	ErrInvalidPreference   = errors.New("matcher: preference list contains a non-positive id")
	ErrDuplicatePreference = errors.New("matcher: preference list contains a duplicate id")
)

// Errors returned by Run, Result and Verify.
var (
	ErrUnknownReference      = errors.New("matcher: preference list references an unregistered id")
	ErrNotRun                = errors.New("matcher: Run has not been called")
	ErrUnknownApplicant      = errors.New("matcher: matching references an unknown applicant")
	ErrUnknownProgram        = errors.New("matcher: matching references an unknown program")
	ErrNotMutuallyAcceptable = errors.New("matcher: matching contains a pair that is not mutually acceptable")
	ErrOverCapacity          = errors.New("matcher: program is assigned more applicants than its capacity")
)

// Pair identifies one applicant-program pair, e.g. a blocking pair.
type Pair struct {
	Applicant int
	Program   int
}

type program struct {
	capacity int
	prefs    []int
}

// Matcher is a capacitated two-sided matching instance. All methods are
// safe for concurrent use; the result is equivalent to some serial order.
type Matcher struct {
	mu         sync.Mutex
	applicants map[int][]int
	programs   map[int]program
	frozen     bool
	ran        bool
	result     map[int]int
	proposals  int
}

// New returns an empty Matcher ready for registration.
func New() *Matcher {
	return &Matcher{
		applicants: make(map[int][]int),
		programs:   make(map[int]program),
	}
}

// AddApplicant registers an applicant with a strictly ordered preference
// list of program ids (most preferred first); only listed programs are
// acceptable. Rejection reasons, first match wins: frozen registry, id < 1,
// id already registered, preference item < 1, duplicate preference item.
func (m *Matcher) AddApplicant(id int, prefs []int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return ErrFrozen
	}
	if id < 1 {
		return ErrInvalidID
	}
	if _, ok := m.applicants[id]; ok {
		return ErrDuplicateID
	}
	if err := checkPrefs(prefs); err != nil {
		return err
	}
	m.applicants[id] = append([]int(nil), prefs...)
	return nil
}

// AddProgram registers a program with a capacity and a strictly ordered
// preference list of applicant ids; only listed applicants are acceptable.
// Rejection reasons, first match wins: frozen to report is the first of:
// frozen registry, id < 1, capacity < 1, id already registered,
// preference item < 1, duplicate preference item.
func (m *Matcher) AddProgram(id int, capacity int, prefs []int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return ErrFrozen
	}
	if id < 1 {
		return ErrInvalidID
	}
	if capacity < 1 {
		return ErrInvalidCapacity
	}
	if _, ok := m.programs[id]; ok {
		return ErrDuplicateID
	}
	if err := checkPrefs(prefs); err != nil {
		return err
	}
	m.programs[id] = program{capacity: capacity, prefs: append([]int(nil), prefs...)}
	return nil
}

func checkPrefs(prefs []int) error {
	seen := make(map[int]struct{}, len(prefs))
	for _, id := range prefs {
		if id < 1 {
			return ErrInvalidPreference
		}
		if _, ok := seen[id]; ok {
			return ErrDuplicatePreference
		}
		seen[id] = struct{}{}
	}
	return nil
}

// Run validates cross-references, freezes the registry and computes the
// applicant-proposing deferred acceptance matching. Calling Run again after
// a successful run returns the same result without recomputing.
func (m *Matcher) Run() (map[int]int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ran {
		return copyResult(m.result), m.proposals, nil
	}
	if err := m.checkReferencesLocked(); err != nil {
		return nil, 0, err
	}
	m.frozen = true
	m.result, m.proposals = m.computeLocked()
	m.ran = true
	return copyResult(m.result), m.proposals, nil
}

// Result returns the computed matching (every applicant id mapped to its
// program id, 0 when unmatched) and the total number of proposals made.
// It reports ErrNotRun before Run has completed.
func (m *Matcher) Result() (map[int]int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ran {
		return nil, 0, ErrNotRun
	}
	return copyResult(m.result), m.proposals, nil
}

// Verify checks a proposed matching (applicant id -> program id; missing
// keys and 0 values mean unmatched) and returns its blocking pairs sorted
// by (applicant, program) ascending.
func (m *Matcher) Verify(match map[int]int) ([]Pair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ran {
		return nil, ErrNotRun
	}
	return m.verifyLocked(match)
}

func (m *Matcher) checkReferencesLocked() error {
	for _, a := range sortedApplicantIDs(m.applicants) {
		for _, p := range m.applicants[a] {
			if _, ok := m.programs[p]; !ok {
				return ErrUnknownReference
			}
		}
	}
	for _, p := range sortedProgramIDs(m.programs) {
		for _, a := range m.programs[p].prefs {
			if _, ok := m.applicants[a]; !ok {
				return ErrUnknownReference
			}
		}
	}
	return nil
}

func (m *Matcher) computeLocked() (map[int]int, int) {
	ranks := make(map[int]map[int]int, len(m.programs))
	for pid, p := range m.programs {
		rank := make(map[int]int, len(p.prefs))
		for i, a := range p.prefs {
			rank[a] = i
		}
		ranks[pid] = rank
	}

	next := make(map[int]int, len(m.applicants))
	held := make(map[int][]int, len(m.programs))
	queue := sortedApplicantIDs(m.applicants)
	proposals := 0

	for len(queue) > 0 {
		a := queue[0]
		queue = queue[1:]
		prefs := m.applicants[a]
		if next[a] >= len(prefs) {
			continue
		}
		p := prefs[next[a]]
		next[a]++
		proposals++
		pr, ok := m.programs[p]
		if !ok {
			queue = append(queue, a)
			continue
		}
		if _, acceptable := ranks[p][a]; !acceptable {
			queue = append(queue, a)
			continue
		}
		held[p] = append(held[p], a)
		if len(held[p]) > pr.capacity {
			worst := held[p][0]
			for _, x := range held[p][1:] {
				if ranks[p][x] > ranks[p][worst] {
					worst = x
				}
			}
			kept := held[p][:0]
			for _, x := range held[p] {
				if x != worst {
					kept = append(kept, x)
				}
			}
			held[p] = kept
			queue = append(queue, worst)
		}
	}

	result := make(map[int]int, len(m.applicants))
	for id := range m.applicants {
		result[id] = 0
	}
	for pid, holders := range held {
		for _, a := range holders {
			result[a] = pid
		}
	}
	return result, proposals
}

func (m *Matcher) verifyLocked(match map[int]int) ([]Pair, error) {
	keys := make([]int, 0, len(match))
	for a := range match {
		keys = append(keys, a)
	}
	sort.Ints(keys)

	for _, a := range keys {
		if _, ok := m.applicants[a]; !ok {
			return nil, ErrUnknownApplicant
		}
		if p := match[a]; p != 0 {
			if _, ok := m.programs[p]; !ok {
				return nil, ErrUnknownProgram
			}
		}
	}

	for _, a := range keys {
		p := match[a]
		if p == 0 {
			continue
		}
		if !m.mutuallyAcceptableLocked(a, p) {
			return nil, ErrNotMutuallyAcceptable
		}
	}

	counts := make(map[int]int, len(m.programs))
	for _, a := range keys {
		if p := match[a]; p != 0 {
			counts[p]++
		}
	}
	for _, pid := range sortedProgramIDs(m.programs) {
		if counts[pid] > m.programs[pid].capacity {
			return nil, ErrOverCapacity
		}
	}

	appRank := make(map[int]map[int]int, len(m.applicants))
	for id, prefs := range m.applicants {
		rank := make(map[int]int, len(prefs))
		for i, p := range prefs {
			rank[p] = i
		}
		appRank[id] = rank
	}
	progRank := make(map[int]map[int]int, len(m.programs))
	for pid, p := range m.programs {
		rank := make(map[int]int, len(p.prefs))
		for i, a := range p.prefs {
			rank[a] = i
		}
		progRank[pid] = rank
	}

	pairs := []Pair{}
	for _, a := range sortedApplicantIDs(m.applicants) {
		cur := match[a]
		for _, pid := range sortedProgramIDs(m.programs) {
			if !m.mutuallyAcceptableLocked(a, pid) {
				continue
			}
			if cur == pid {
				continue
			}
			if cur != 0 && appRank[a][cur] < appRank[a][pid] {
				continue
			}
			if counts[pid] >= m.programs[pid].capacity {
				worst := -1
				for _, b := range keys {
					if match[b] != pid {
						continue
					}
					if worst == -1 || progRank[pid][b] > progRank[pid][worst] {
						worst = b
					}
				}
				if worst == -1 || progRank[pid][worst] < progRank[pid][a] {
					continue
				}
			}
			pairs = append(pairs, Pair{Applicant: a, Program: pid})
		}
	}
	return pairs, nil
}

func (m *Matcher) mutuallyAcceptableLocked(a, p int) bool {
	pr, ok := m.programs[p]
	if !ok {
		return false
	}
	if !containsInt(pr.prefs, a) {
		return false
	}
	prefs, ok := m.applicants[a]
	if !ok {
		return false
	}
	return containsInt(prefs, p)
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func copyResult(result map[int]int) map[int]int {
	out := make(map[int]int, len(result))
	for k, v := range result {
		out[k] = v
	}
	return out
}

func sortedApplicantIDs(applicants map[int][]int) []int {
	ids := make([]int, 0, len(applicants))
	for id := range applicants {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func sortedProgramIDs(programs map[int]program) []int {
	ids := make([]int, 0, len(programs))
	for id := range programs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
