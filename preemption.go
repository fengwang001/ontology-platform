package ontology

import (
	"errors"
	"math/big"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrPodExists       = errors.New("pod already exists")
	ErrNodeExists      = errors.New("node already exists")
	ErrNodeNotFound    = errors.New("node not found")
	ErrCapacityFull    = errors.New("insufficient node capacity")
	ErrNoPreemption    = errors.New("preemption is unnecessary")
	ErrNoFeasibleNode  = errors.New("no feasible node")
)

type Pod struct {
	ID          string
	Priority    int32
	Request     uint64
	BudgetGroup string
}

type PreemptionResult struct {
	Node    string
	Victims []string
}

type Selector struct {
	mu      sync.Mutex
	nodes   map[string]*nodeState
	pods    map[string]*placedPod
	budgets map[string]uint64
}

type nodeState struct {
	name     string
	capacity uint64
	used     uint64
	pods     []*placedPod
}

type placedPod struct {
	pod  Pod
	node string
}

type nodeEvaluation struct {
	node             string
	victims          []*placedPod
	violatingVictims int
	highestPriority  int32
	priorityScore    *big.Int
}

func NewSelector() *Selector {
	return &Selector{
		nodes:   make(map[string]*nodeState),
		pods:    make(map[string]*placedPod),
		budgets: make(map[string]uint64),
	}
}

func (s *Selector) AddNode(name string, capacity uint64) error {
	if name == "" || capacity == 0 || capacity > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.nodes[name]; exists {
		return ErrNodeExists
	}
	s.nodes[name] = &nodeState{name: name, capacity: capacity}
	return nil
}

func (s *Selector) SetBudget(group string, allowed uint64) error {
	if group == "" || allowed > 1_000_000_000 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.budgets[group] = allowed
	return nil
}

func (s *Selector) Place(pod Pod, node string) error {
	if !validPod(pod) || node == "" {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.pods[pod.ID]; exists {
		return ErrPodExists
	}
	target, exists := s.nodes[node]
	if !exists {
		return ErrNodeNotFound
	}
	if target.used > target.capacity || target.capacity-target.used < pod.Request {
		return ErrCapacityFull
	}
	s.addPodLocked(pod, node)
	return nil
}

func (s *Selector) Preempt(pod Pod) (PreemptionResult, error) {
	if !validPod(pod) {
		return PreemptionResult{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.pods[pod.ID]; exists {
		return PreemptionResult{}, ErrPodExists
	}

	nodeNames := make([]string, 0, len(s.nodes))
	for name := range s.nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	for _, name := range nodeNames {
		current := s.nodes[name]
		if current.capacity-current.used >= pod.Request {
			return PreemptionResult{}, ErrNoPreemption
		}
	}

	var best *nodeEvaluation
	for _, name := range nodeNames {
		evaluation, feasible := s.evaluateNodeLocked(s.nodes[name], pod, s.budgetSnapshotLocked())
		if !feasible {
			continue
		}
		if best == nil || betterEvaluation(evaluation, best) {
			best = evaluation
		}
	}
	if best == nil {
		return PreemptionResult{}, ErrNoFeasibleNode
	}

	target := s.nodes[best.node]
	victimIDs := make([]string, 0, len(best.victims))
	victimByID := make(map[string]struct{}, len(best.victims))
	sortVictimsByOutput(best.victims)
	for _, victim := range best.victims {
		victimIDs = append(victimIDs, victim.pod.ID)
		victimByID[victim.pod.ID] = struct{}{}
	}

	remaining := make([]*placedPod, 0, len(target.pods)-len(best.victims))
	for _, entry := range target.pods {
		if _, removed := victimByID[entry.pod.ID]; removed {
			if entry.pod.BudgetGroup != "" {
				deductBudgetLocked(s.budgets, entry.pod.BudgetGroup)
			}
			delete(s.pods, entry.pod.ID)
			continue
		}
		remaining = append(remaining, entry)
	}
	target.pods = remaining
	target.used -= sumRequests(best.victims)
	s.addPodLocked(pod, target.name)

	return PreemptionResult{Node: target.name, Victims: victimIDs}, nil
}

func validPod(pod Pod) bool {
	return pod.ID != "" && pod.Request >= 1 && pod.Request <= 1_000_000_000_000
}

func (s *Selector) addPodLocked(pod Pod, node string) {
	entry := &placedPod{pod: pod, node: node}
	s.pods[pod.ID] = entry
	target := s.nodes[node]
	target.pods = append(target.pods, entry)
	target.used += pod.Request
}

func (s *Selector) budgetSnapshotLocked() map[string]uint64 {
	snapshot := make(map[string]uint64, len(s.budgets))
	for group, allowed := range s.budgets {
		snapshot[group] = allowed
	}
	return snapshot
}

func (s *Selector) evaluateNodeLocked(current *nodeState, incoming Pod, budgets map[string]uint64) (*nodeEvaluation, bool) {
	var protectedRequest uint64
	candidates := make([]*placedPod, 0, len(current.pods))
	for _, entry := range current.pods {
		if entry.pod.Priority >= incoming.Priority {
			protectedRequest += entry.pod.Request
		} else {
			candidates = append(candidates, entry)
		}
	}
	if protectedRequest > current.capacity || current.capacity-protectedRequest < incoming.Request {
		return nil, false
	}

	sortCandidatesByImportance(candidates)
	violating := make([]*placedPod, 0)
	nonViolating := make([]*placedPod, 0)
	for _, entry := range candidates {
		if entry.pod.BudgetGroup == "" {
			nonViolating = append(nonViolating, entry)
			continue
		}
		allowed := budgets[entry.pod.BudgetGroup]
		if allowed == 0 {
			violating = append(violating, entry)
			continue
		}
		budgets[entry.pod.BudgetGroup] = allowed - 1
		nonViolating = append(nonViolating, entry)
	}

	free := current.capacity - protectedRequest
	victims := make([]*placedPod, 0, len(candidates))
	for _, group := range [][]*placedPod{violating, nonViolating} {
		for _, entry := range group {
			if entry.pod.Request <= free && free-entry.pod.Request >= incoming.Request {
				free -= entry.pod.Request
				continue
			}
			victims = append(victims, entry)
		}
	}

	evaluation := &nodeEvaluation{
		node:            current.name,
		victims:         victims,
		highestPriority: minInt32,
		priorityScore:   big.NewInt(0),
	}
	violatingSet := make(map[*placedPod]struct{}, len(violating))
	for _, entry := range violating {
		violatingSet[entry] = struct{}{}
	}
	for _, victim := range victims {
		if _, isViolating := violatingSet[victim]; isViolating {
			evaluation.violatingVictims++
		}
		if victim.pod.Priority > evaluation.highestPriority {
			evaluation.highestPriority = victim.pod.Priority
		}
		evaluation.priorityScore.Add(evaluation.priorityScore, priorityWeight(victim.pod.Priority))
	}
	return evaluation, true
}

const minInt32 = -1 << 31

func priorityWeight(priority int32) *big.Int {
	return big.NewInt(int64(priority) + (1 << 31))
}

func sortCandidatesByImportance(candidates []*placedPod) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].pod.Priority != candidates[j].pod.Priority {
			return candidates[i].pod.Priority > candidates[j].pod.Priority
		}
		return candidates[i].pod.ID < candidates[j].pod.ID
	})
}

func betterEvaluation(candidate, current *nodeEvaluation) bool {
	if candidate.violatingVictims != current.violatingVictims {
		return candidate.violatingVictims < current.violatingVictims
	}
	if candidate.highestPriority != current.highestPriority {
		return candidate.highestPriority < current.highestPriority
	}
	if score := candidate.priorityScore.Cmp(current.priorityScore); score != 0 {
		return score < 0
	}
	if len(candidate.victims) != len(current.victims) {
		return len(candidate.victims) < len(current.victims)
	}
	return candidate.node < current.node
}

func sumRequests(entries []*placedPod) uint64 {
	var total uint64
	for _, entry := range entries {
		total += entry.pod.Request
	}
	return total
}

func deductBudgetLocked(budgets map[string]uint64, group string) {
	if budgets[group] > 0 {
		budgets[group]--
	}
}

func sortVictimsByOutput(victims []*placedPod) {
	sort.Slice(victims, func(i, j int) bool {
		if victims[i].pod.Priority != victims[j].pod.Priority {
			return victims[i].pod.Priority < victims[j].pod.Priority
		}
		return victims[i].pod.ID < victims[j].pod.ID
	})
}
