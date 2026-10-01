package ontology

import (
	"sort"
	"sync"
)

type Effect string

const (
	NoSchedule       Effect = "NoSchedule"
	PreferNoSchedule Effect = "PreferNoSchedule"
	NoExecute        Effect = "NoExecute"
)

type TolerationOperator string

const (
	EqualOperator  TolerationOperator = "Equal"
	ExistsOperator TolerationOperator = "Exists"
)

type Taint struct {
	Key    string
	Value  string
	Effect Effect
}

type Toleration struct {
	Key      string
	Operator TolerationOperator
	Value    string
	Effect   Effect
	Seconds  int64
}

type ErrorCode string

const (
	ErrInvalidConfig        ErrorCode = "invalid_config"
	ErrInvalidArgument      ErrorCode = "invalid_argument"
	ErrClockBacktrack       ErrorCode = "clock_backtrack"
	ErrNodeExists           ErrorCode = "node_exists"
	ErrNodeNotFound         ErrorCode = "node_not_found"
	ErrPodExists            ErrorCode = "pod_exists"
	ErrTaintNotFound        ErrorCode = "taint_not_found"
	ErrUnschedulableTaint   ErrorCode = "unschedulable_taint"
	ErrInsufficientCapacity ErrorCode = "insufficient_capacity"
)

type OperationError struct {
	Code   ErrorCode
	Key    string
	Effect Effect
}

func (e *OperationError) Error() string {
	return string(e.Code)
}

type Manager struct {
	mu               sync.Mutex
	gracePeriodMS    int64
	evictionsPerTick int
	lastNow          int64
	nodes            map[string]*nodeState
	pods             map[string]*podBinding
}

type nodeState struct {
	name     string
	maxPods  int
	taints   map[taintKey]*TaintInstance
	bindings map[string]*podBinding
}

type taintKey struct {
	key    string
	effect Effect
}

type TaintInstance struct {
	taint   Taint
	addedAt int64
}

type podBinding struct {
	id          string
	node        string
	tolerations []Toleration
	terminating bool
	releaseAt   int64
}

func NewManager(gracePeriodMS int64, evictionsPerTick int) (*Manager, error) {
	if gracePeriodMS < 0 || gracePeriodMS > 1_000_000_000_000 ||
		evictionsPerTick < 1 || evictionsPerTick > 1_000_000 {
		return nil, &OperationError{Code: ErrInvalidConfig}
	}
	return &Manager{
		gracePeriodMS:    gracePeriodMS,
		evictionsPerTick: evictionsPerTick,
		nodes:            make(map[string]*nodeState),
		pods:             make(map[string]*podBinding),
	}, nil
}

func (m *Manager) AddNode(name string, maxPods int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if name == "" || maxPods < 1 || maxPods > 1_000_000 {
		return &OperationError{Code: ErrInvalidArgument}
	}
	if _, ok := m.nodes[name]; ok {
		return &OperationError{Code: ErrNodeExists}
	}
	m.nodes[name] = &nodeState{
		name:     name,
		maxPods:  maxPods,
		taints:   make(map[taintKey]*TaintInstance),
		bindings: make(map[string]*podBinding),
	}
	return nil
}

func (m *Manager) Taint(nodeName string, taint Taint, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if nodeName == "" {
		return &OperationError{Code: ErrInvalidArgument}
	}
	if err := validateTaint(taint); err != nil {
		return err
	}
	if err := validateNow(now); err != nil {
		return err
	}
	if now < m.lastNow {
		return &OperationError{Code: ErrClockBacktrack}
	}
	node, ok := m.nodes[nodeName]
	if !ok {
		return &OperationError{Code: ErrNodeNotFound}
	}

	m.releaseAllExpired(now)
	key := taintKey{key: taint.Key, effect: taint.Effect}
	if existing, ok := node.taints[key]; ok {
		existing.taint.Value = taint.Value
	} else {
		node.taints[key] = &TaintInstance{
			taint:   taint,
			addedAt: now,
		}
	}
	m.lastNow = now
	return nil
}

func (m *Manager) Untaint(nodeName, key string, effect Effect, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if nodeName == "" || key == "" || !validEffect(effect) {
		return &OperationError{Code: ErrInvalidArgument}
	}
	if err := validateNow(now); err != nil {
		return err
	}
	if now < m.lastNow {
		return &OperationError{Code: ErrClockBacktrack}
	}
	node, ok := m.nodes[nodeName]
	if !ok {
		return &OperationError{Code: ErrNodeNotFound}
	}

	taintID := taintKey{key: key, effect: effect}
	if _, ok := node.taints[taintID]; !ok {
		return &OperationError{Code: ErrTaintNotFound}
	}
	m.releaseAllExpired(now)
	delete(node.taints, taintID)
	m.lastNow = now
	return nil
}

func (m *Manager) Schedule(podID, nodeName string, tolerations []Toleration, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if podID == "" || nodeName == "" {
		return &OperationError{Code: ErrInvalidArgument}
	}
	if err := validateTolerations(tolerations); err != nil {
		return err
	}
	if err := validateNow(now); err != nil {
		return err
	}
	if now < m.lastNow {
		return &OperationError{Code: ErrClockBacktrack}
	}
	if pod, ok := m.pods[podID]; ok && activeAt(pod, now) {
		return &OperationError{Code: ErrPodExists}
	}
	node, ok := m.nodes[nodeName]
	if !ok {
		return &OperationError{Code: ErrNodeNotFound}
	}

	if first, ok := firstUnschedulableTaint(node.taints, tolerations); ok {
		return &OperationError{
			Code:   ErrUnschedulableTaint,
			Key:    first.key,
			Effect: first.effect,
		}
	}

	if activePodCount(node, now) >= node.maxPods {
		return &OperationError{Code: ErrInsufficientCapacity}
	}

	m.releaseAllExpired(now)
	binding := &podBinding{
		id:          podID,
		node:        nodeName,
		tolerations: append([]Toleration(nil), tolerations...),
	}
	node.bindings[podID] = binding
	m.pods[podID] = binding
	m.lastNow = now
	return nil
}

func (m *Manager) Tick(now int64) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateNow(now); err != nil {
		return nil, err
	}
	if now < m.lastNow {
		return nil, &OperationError{Code: ErrClockBacktrack}
	}

	type eviction struct {
		podID    string
		deadline int64
	}

	var evicted []eviction
	m.releaseAllExpired(now)
	for _, node := range m.nodes {
		candidates := make([]eviction, 0, len(node.bindings))
		for podID, pod := range node.bindings {
			if pod.terminating {
				continue
			}
			deadline, ok := evictionTime(node.taints, pod.tolerations)
			if ok && deadline <= now {
				candidates = append(candidates, eviction{podID: podID, deadline: deadline})
			}
		}

		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].deadline != candidates[j].deadline {
				return candidates[i].deadline < candidates[j].deadline
			}
			return candidates[i].podID < candidates[j].podID
		})
		if len(candidates) > m.evictionsPerTick {
			candidates = candidates[:m.evictionsPerTick]
		}
		evicted = append(evicted, candidates...)
	}

	sort.Slice(evicted, func(i, j int) bool {
		if evicted[i].deadline != evicted[j].deadline {
			return evicted[i].deadline < evicted[j].deadline
		}
		return evicted[i].podID < evicted[j].podID
	})

	podIDs := make([]string, 0, len(evicted))
	for _, item := range evicted {
		pod := m.pods[item.podID]
		pod.terminating = true
		pod.releaseAt = now + m.gracePeriodMS
		podIDs = append(podIDs, item.podID)
	}
	m.lastNow = now
	return podIDs, nil
}

func (m *Manager) releaseAllExpired(now int64) {
	for _, node := range m.nodes {
		for podID, pod := range node.bindings {
			if pod.terminating && pod.releaseAt <= now {
				delete(node.bindings, podID)
				delete(m.pods, podID)
			}
		}
	}
}

func activeAt(pod *podBinding, now int64) bool {
	return !pod.terminating || pod.releaseAt > now
}

func activePodCount(node *nodeState, now int64) int {
	count := 0
	for _, pod := range node.bindings {
		if activeAt(pod, now) {
			count++
		}
	}
	return count
}

func validEffect(effect Effect) bool {
	return effect == NoSchedule || effect == PreferNoSchedule || effect == NoExecute
}

func validateNow(now int64) error {
	if now < 0 || now > 100_000_000_000_000_000 {
		return &OperationError{Code: ErrInvalidArgument}
	}
	return nil
}

func validateTaint(taint Taint) error {
	if taint.Key == "" || !validEffect(taint.Effect) {
		return &OperationError{Code: ErrInvalidArgument}
	}
	return nil
}

func validateTolerations(tolerations []Toleration) error {
	for _, toleration := range tolerations {
		switch toleration.Operator {
		case EqualOperator:
			if toleration.Key == "" {
				return &OperationError{Code: ErrInvalidArgument}
			}
		case ExistsOperator:
			if toleration.Value != "" {
				return &OperationError{Code: ErrInvalidArgument}
			}
		default:
			return &OperationError{Code: ErrInvalidArgument}
		}

		if toleration.Effect != "" && !validEffect(toleration.Effect) {
			return &OperationError{Code: ErrInvalidArgument}
		}
		if toleration.Seconds != -1 {
			if toleration.Effect != NoExecute || toleration.Seconds < 0 ||
				toleration.Seconds > 100_000_000_000_000 {
				return &OperationError{Code: ErrInvalidArgument}
			}
		}
	}
	return nil
}

func firstUnschedulableTaint(taints map[taintKey]*TaintInstance, tolerations []Toleration) (taintKey, bool) {
	var blocked []taintKey
	for key := range taints {
		if key.effect == NoSchedule || key.effect == NoExecute {
			blocked = append(blocked, key)
		}
	}
	sort.Slice(blocked, func(i, j int) bool {
		if blocked[i].key != blocked[j].key {
			return blocked[i].key < blocked[j].key
		}
		return blocked[i].effect < blocked[j].effect
	})
	for _, key := range blocked {
		if !tolerationMatchesAny(taints[key].taint, tolerations) {
			return key, true
		}
	}
	return taintKey{}, false
}

func tolerationMatchesAny(taint Taint, tolerations []Toleration) bool {
	for _, toleration := range tolerations {
		if tolerationMatches(taint, toleration) {
			return true
		}
	}
	return false
}

func tolerationMatches(taint Taint, toleration Toleration) bool {
	if toleration.Key == "" {
		if toleration.Operator != ExistsOperator {
			return false
		}
	} else if toleration.Key != taint.Key {
		return false
	}
	if toleration.Effect != "" && toleration.Effect != taint.Effect {
		return false
	}
	if toleration.Key == "" {
		return true
	}
	return toleration.Operator == ExistsOperator || toleration.Value == taint.Value
}

func evictionTime(taints map[taintKey]*TaintInstance, tolerations []Toleration) (int64, bool) {
	minimum := int64(0)
	found := false
	for _, instance := range taints {
		if instance.taint.Effect != NoExecute {
			continue
		}
		deadline, finite := taintEvictionTime(instance, tolerations)
		if !finite {
			continue
		}
		if !found || deadline < minimum {
			minimum = deadline
			found = true
		}
	}
	return minimum, found
}

func taintEvictionTime(instance *TaintInstance, tolerations []Toleration) (int64, bool) {
	for _, toleration := range tolerations {
		if !tolerationMatches(instance.taint, toleration) {
			continue
		}
		if toleration.Seconds == -1 {
			return 0, false
		}
		return instance.addedAt + toleration.Seconds*1000, true
	}
	return instance.addedAt, true
}
