package ontology

import (
	"errors"
	"sort"
)

type naiveManager struct {
	gracePeriodMS    int64
	evictionsPerTick int
	lastNow          int64
	nodes            map[string]*naiveNode
	pods             map[string]*naivePod
}

type naiveNode struct {
	name     string
	maxPods  int
	taints   map[naiveTaintKey]*naiveTaint
	bindings map[string]*naivePod
}

type naiveTaintKey struct {
	key    string
	effect Effect
}

type naiveTaint struct {
	value   string
	addedAt int64
}

type naivePod struct {
	id          string
	node        string
	tolerations []Toleration
	terminating bool
	releaseAt   int64
}

type randomOp struct {
	kind        string
	now         int64
	node        string
	pod         string
	key         string
	value       string
	effect      Effect
	tolerations []Toleration
	maxPods     int
}

type naiveCandidate struct {
	id       string
	deadline int64
}

func newNaiveManager(gracePeriodMS int64, evictionsPerTick int) *naiveManager {
	return &naiveManager{
		gracePeriodMS:    gracePeriodMS,
		evictionsPerTick: evictionsPerTick,
		nodes:            make(map[string]*naiveNode),
		pods:             make(map[string]*naivePod),
	}
}

func (n *naiveManager) release(now int64) {
	for _, node := range n.nodes {
		for id, pod := range node.bindings {
			if pod.terminating && pod.releaseAt <= now {
				delete(node.bindings, id)
				delete(n.pods, id)
			}
		}
	}
}

func (n *naiveManager) addNode(op randomOp) ErrorCode {
	if op.node == "" || op.maxPods < 1 || op.maxPods > 1_000_000 {
		return ErrInvalidArgument
	}
	if _, ok := n.nodes[op.node]; ok {
		return ErrNodeExists
	}
	n.nodes[op.node] = &naiveNode{
		name:     op.node,
		maxPods:  op.maxPods,
		taints:   make(map[naiveTaintKey]*naiveTaint),
		bindings: make(map[string]*naivePod),
	}
	return ""
}

func (n *naiveManager) validTimedOp(op randomOp) ErrorCode {
	if op.now < 0 || op.now > 100_000_000_000_000_000 {
		return ErrInvalidArgument
	}
	if op.now < n.lastNow {
		return ErrClockBacktrack
	}
	return ""
}

func (n *naiveManager) taint(op randomOp) ErrorCode {
	if op.key == "" || !validEffect(op.effect) {
		return ErrInvalidArgument
	}
	if code := n.validTimedOp(op); code != "" {
		return code
	}
	node, ok := n.nodes[op.node]
	if !ok {
		return ErrNodeNotFound
	}
	n.release(op.now)
	key := naiveTaintKey{key: op.key, effect: op.effect}
	if existing, ok := node.taints[key]; ok {
		existing.value = op.value
	} else {
		node.taints[key] = &naiveTaint{value: op.value, addedAt: op.now}
	}
	n.lastNow = op.now
	return ""
}

func (n *naiveManager) untaint(op randomOp) ErrorCode {
	if op.key == "" || !validEffect(op.effect) {
		return ErrInvalidArgument
	}
	if code := n.validTimedOp(op); code != "" {
		return code
	}
	node, ok := n.nodes[op.node]
	if !ok {
		return ErrNodeNotFound
	}
	key := naiveTaintKey{key: op.key, effect: op.effect}
	if _, ok := node.taints[key]; !ok {
		return ErrTaintNotFound
	}
	n.release(op.now)
	delete(node.taints, key)
	n.lastNow = op.now
	return ""
}

func naiveMatches(key string, value string, effect Effect, toleration Toleration) bool {
	if toleration.Key == "" {
		if toleration.Operator != ExistsOperator || (toleration.Effect != "" && toleration.Effect != effect) {
			return false
		}
		return true
	}
	if toleration.Key != key {
		return false
	}
	if toleration.Effect != "" && toleration.Effect != effect {
		return false
	}
	return toleration.Operator == ExistsOperator || toleration.Value == value
}

func naiveDeadline(taints map[naiveTaintKey]*naiveTaint, tolerations []Toleration) (int64, bool) {
	minimum := int64(0)
	found := false
	for key, taint := range taints {
		if key.effect != NoExecute {
			continue
		}
		localDeadline := taint.addedAt
		finite := true
		for _, toleration := range tolerations {
			if !naiveMatches(key.key, taint.value, key.effect, toleration) {
				continue
			}
			if toleration.Seconds == -1 {
				finite = false
			} else {
				localDeadline = taint.addedAt + toleration.Seconds*1000
				finite = true
			}
			break
		}
		if finite && (!found || localDeadline < minimum) {
			minimum = localDeadline
			found = true
		}
	}
	return minimum, found
}

func naiveActiveAt(pod *naivePod, now int64) bool {
	return !pod.terminating || pod.releaseAt > now
}

func naiveActiveCount(node *naiveNode, now int64) int {
	count := 0
	for _, pod := range node.bindings {
		if naiveActiveAt(pod, now) {
			count++
		}
	}
	return count
}

func (n *naiveManager) schedule(op randomOp) ErrorCode {
	if op.pod == "" || op.node == "" {
		return ErrInvalidArgument
	}
	if err := validateTolerations(op.tolerations); err != nil {
		var operationErr *OperationError
		errors.As(err, &operationErr)
		return operationErr.Code
	}
	if code := n.validTimedOp(op); code != "" {
		return code
	}
	if pod, ok := n.pods[op.pod]; ok && naiveActiveAt(pod, op.now) {
		return ErrPodExists
	}
	node, ok := n.nodes[op.node]
	if !ok {
		return ErrNodeNotFound
	}

	var blocked []naiveTaintKey
	for key := range node.taints {
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
		matches := false
		for _, toleration := range op.tolerations {
			if naiveMatches(key.key, node.taints[key].value, key.effect, toleration) {
				matches = true
				break
			}
		}
		if !matches {
			return ErrUnschedulableTaint
		}
	}
	if naiveActiveCount(node, op.now) >= node.maxPods {
		return ErrInsufficientCapacity
	}
	n.release(op.now)
	pod := &naivePod{
		id:          op.pod,
		node:        op.node,
		tolerations: append([]Toleration(nil), op.tolerations...),
	}
	node.bindings[pod.id] = pod
	n.pods[pod.id] = pod
	n.lastNow = op.now
	return ""
}

func (n *naiveManager) tick(op randomOp) ([]string, ErrorCode) {
	if code := n.validTimedOp(op); code != "" {
		return nil, code
	}
	n.release(op.now)
	var all []naiveCandidate
	for _, node := range n.nodes {
		var candidates []naiveCandidate
		for id, pod := range node.bindings {
			if pod.terminating {
				continue
			}
			deadline, ok := naiveDeadline(node.taints, pod.tolerations)
			if ok && deadline <= op.now {
				candidates = append(candidates, naiveCandidate{id: id, deadline: deadline})
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].deadline != candidates[j].deadline {
				return candidates[i].deadline < candidates[j].deadline
			}
			return candidates[i].id < candidates[j].id
		})
		if len(candidates) > n.evictionsPerTick {
			candidates = candidates[:n.evictionsPerTick]
		}
		all = append(all, candidates...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].deadline != all[j].deadline {
			return all[i].deadline < all[j].deadline
		}
		return all[i].id < all[j].id
	})
	ids := make([]string, 0, len(all))
	for _, candidate := range all {
		pod := n.pods[candidate.id]
		pod.terminating = true
		pod.releaseAt = op.now + n.gracePeriodMS
		ids = append(ids, candidate.id)
	}
	n.lastNow = op.now
	return ids, ""
}
