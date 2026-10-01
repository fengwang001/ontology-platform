package ontology

import (
	"errors"
	"math/big"
	"sort"
	"sync"
)

type Pod struct {
	ID          string
	Prio        int32
	Req         int64
	BudgetGroup string
}

type RejectReason string

const (
	RejectInvalidArgument  RejectReason = "invalid_argument"
	RejectPodExists        RejectReason = "pod_exists"
	RejectNodeExists       RejectReason = "node_exists"
	RejectNodeNotFound     RejectReason = "node_not_found"
	RejectCapacityExceeded RejectReason = "capacity_exceeded"
	RejectNoPreemption     RejectReason = "no_preemption_needed"
	RejectNoFeasibleNode   RejectReason = "no_feasible_node"
)

type RejectError struct {
	Reason RejectReason
}

func (e *RejectError) Error() string {
	return string(e.Reason)
}

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrPodExists        = errors.New("pod already exists")
	ErrNodeExists       = errors.New("node already exists")
	ErrNodeNotFound     = errors.New("node not found")
	ErrCapacityExceeded = errors.New("capacity exceeded")
	ErrNoPreemption     = errors.New("no preemption needed")
	ErrNoFeasibleNode   = errors.New("no feasible node")
)

func NewSelector() *Selector {
	return &Selector{
		nodes:   make(map[string]*nodeState),
		budgets: make(map[string]int64),
	}
}

func (s *Selector) AddNode(name string, capacity int64) error {
	if name == "" || capacity < 1 || capacity > 1_000_000_000_000 {
		return reject(RejectInvalidArgument)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.nodes[name]; ok {
		return reject(RejectNodeExists)
	}

	s.nodes[name] = &nodeState{capacity: capacity}
	return nil
}

func (s *Selector) SetBudget(group string, allowed int64) error {
	if group == "" || allowed < 0 || allowed > 1_000_000_000 {
		return reject(RejectInvalidArgument)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.budgets[group] = allowed
	return nil
}

func (s *Selector) Place(pod Pod, nodeName string) error {
	if err := validatePod(pod); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.pods[pod.ID]; ok {
		return reject(RejectPodExists)
	}
	node, ok := s.nodes[nodeName]
	if !ok {
		return reject(RejectNodeNotFound)
	}
	if node.used+pod.Req > node.capacity {
		return reject(RejectCapacityExceeded)
	}

	node.pods = append(node.pods, pod)
	node.used += pod.Req
	if s.pods == nil {
		s.pods = make(map[string]podLocation)
	}
	s.pods[pod.ID] = podLocation{node: nodeName}
	return nil
}

func (s *Selector) Preempt(pod Pod) (string, []string, error) {
	if err := validatePod(pod); err != nil {
		return "", nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.pods[pod.ID]; ok {
		return "", nil, reject(RejectPodExists)
	}

	hasFeasibleNode := false
	for name, node := range s.nodes {
		if node.capacity-node.used >= pod.Req {
			return "", nil, reject(RejectNoPreemption)
		}
		_ = name
		if node.capacity-reservedFor(node, pod.Prio) >= pod.Req {
			hasFeasibleNode = true
		}
	}
	if !hasFeasibleNode {
		return "", nil, reject(RejectNoFeasibleNode)
	}

	nodeNames := make([]string, 0, len(s.nodes))
	for name := range s.nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	var best *preemptChoice
	var bestNode string
	for _, nodeName := range nodeNames {
		choice := evaluateNode(s.nodes[nodeName], pod, s.budgets)
		if choice == nil {
			continue
		}
		if best == nil || betterChoice(choice, best) {
			best = choice
			bestNode = nodeName
		}
	}
	if best == nil {
		return "", nil, reject(RejectNoFeasibleNode)
	}

	node := s.nodes[bestNode]
	victims := make(map[string]Pod, len(best.victims))
	remaining := make([]Pod, 0, len(node.pods)-len(best.victims)+1)
	for _, existing := range node.pods {
		if best.victims[existing.ID] {
			victims[existing.ID] = existing
			node.used -= existing.Req
			continue
		}
		remaining = append(remaining, existing)
	}

	for _, victim := range victims {
		if victim.BudgetGroup != "" {
			current := s.budgets[victim.BudgetGroup]
			if current > 0 {
				s.budgets[victim.BudgetGroup] = current - 1
			}
		}
		delete(s.pods, victim.ID)
	}

	remaining = append(remaining, pod)
	node.pods = remaining
	node.used += pod.Req
	if s.pods == nil {
		s.pods = make(map[string]podLocation)
	}
	s.pods[pod.ID] = podLocation{node: bestNode}

	sort.Slice(best.victimPods, func(i, j int) bool {
		left := best.victimPods[i]
		right := best.victimPods[j]
		if left.Prio != right.Prio {
			return left.Prio < right.Prio
		}
		return left.ID < right.ID
	})

	victimIDs := make([]string, len(best.victimPods))
	for i, victim := range best.victimPods {
		victimIDs[i] = victim.ID
	}
	return bestNode, victimIDs, nil
}

type Selector struct {
	mu      sync.Mutex
	nodes   map[string]*nodeState
	pods    map[string]podLocation
	budgets map[string]int64
}

type nodeState struct {
	capacity int64
	used     int64
	pods     []Pod
}

type podLocation struct {
	node string
}

type preemptChoice struct {
	victims        map[string]bool
	victimPods     []Pod
	violatingCount int
	highestPrio    int32
	score          *big.Int
	victimCount    int
}

func validatePod(pod Pod) error {
	if pod.ID == "" || pod.Req < 1 || pod.Req > 1_000_000_000_000 {
		return reject(RejectInvalidArgument)
	}
	if pod.Prio < -(1<<31) || pod.Prio > (1<<31-1) {
		return reject(RejectInvalidArgument)
	}
	return nil
}

func reject(reason RejectReason) error {
	return &RejectError{Reason: reason}
}

func reservedFor(node *nodeState, newPrio int32) int64 {
	var total int64
	for _, pod := range node.pods {
		if pod.Prio >= newPrio {
			total += pod.Req
		}
	}
	return total
}

func evaluateNode(node *nodeState, incoming Pod, globalBudgets map[string]int64) *preemptChoice {
	var reserved int64
	candidates := make([]Pod, 0, len(node.pods))
	for _, pod := range node.pods {
		if pod.Prio >= incoming.Prio {
			reserved += pod.Req
		} else {
			candidates = append(candidates, pod)
		}
	}

	freeCapacity := node.capacity - reserved
	if freeCapacity < incoming.Req {
		return nil
	}

	sortImportance(candidates)
	budgetCopy := cloneBudgets(globalBudgets)
	violating := make([]Pod, 0, len(candidates))
	nonViolating := make([]Pod, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.BudgetGroup == "" {
			nonViolating = append(nonViolating, candidate)
			continue
		}

		budgetCopy[candidate.BudgetGroup]--
		if budgetCopy[candidate.BudgetGroup] < 0 {
			violating = append(violating, candidate)
		} else {
			nonViolating = append(nonViolating, candidate)
		}
	}

	ordered := make([]Pod, 0, len(candidates))
	ordered = append(ordered, violating...)
	ordered = append(ordered, nonViolating...)

	victims := make(map[string]bool, len(candidates))
	victimPods := make([]Pod, 0, len(candidates))
	for _, candidate := range ordered {
		if freeCapacity-candidate.Req >= incoming.Req {
			freeCapacity -= candidate.Req
		} else {
			victims[candidate.ID] = true
			victimPods = append(victimPods, candidate)
		}
	}

	choice := &preemptChoice{
		victims:     victims,
		victimPods:  victimPods,
		victimCount: len(victimPods),
		score:       big.NewInt(0),
	}
	for i, victim := range victimPods {
		if i == 0 || victim.Prio > choice.highestPrio {
			choice.highestPrio = victim.Prio
		}
		if victim.BudgetGroup != "" && budgetCopy[victim.BudgetGroup] < 0 {
			choice.violatingCount++
		}
		choice.score.Add(choice.score, big.NewInt(int64(victim.Prio)+(1<<31)))
	}
	return choice
}

func sortImportance(pods []Pod) {
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Prio != pods[j].Prio {
			return pods[i].Prio > pods[j].Prio
		}
		return pods[i].ID < pods[j].ID
	})
}

func cloneBudgets(budgets map[string]int64) map[string]int64 {
	copyBudgets := make(map[string]int64, len(budgets))
	for group, allowed := range budgets {
		copyBudgets[group] = allowed
	}
	return copyBudgets
}

func betterChoice(left, right *preemptChoice) bool {
	if left.violatingCount != right.violatingCount {
		return left.violatingCount < right.violatingCount
	}
	if left.highestPrio != right.highestPrio {
		return left.highestPrio < right.highestPrio
	}
	if left.score.Cmp(right.score) != 0 {
		return left.score.Cmp(right.score) < 0
	}
	if left.victimCount != right.victimCount {
		return left.victimCount < right.victimCount
	}
	return false
}
