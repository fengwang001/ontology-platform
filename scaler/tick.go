package scaler

import "sort"

type candidate struct {
	migrations []PodMigration
}

func (s *Scaler) Tick(now int64) (TickResult, error) {
	if now < 0 {
		return TickResult{}, ErrInvalidTime
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastNow {
		return TickResult{}, ErrClockRollback
	}
	s.lastNow = now

	nodeNames := make([]string, 0, len(s.nodes))
	for name := range s.nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	candidates := make(map[string]candidate)
	removable := make(map[string]bool)
	simulatedCPU := make(map[string]int64)
	simulatedMem := make(map[string]int64)
	received := make(map[string]bool)
	s.lastTickRemovable = make(map[string]bool)

	for _, name := range nodeNames {
		v := s.nodes[name]
		s.lastTickRemovable[name] = false
		cand, ok := s.evaluateRemovable(v, nodeNames, removable, simulatedCPU, simulatedMem, received)
		if ok {
			candidates[name] = cand
			removable[name] = true
			s.lastTickRemovable[name] = true
			for _, migration := range cand.migrations {
				received[migration.TargetNode] = true
			}
			if v.since == nil {
				current := now
				v.since = &current
			}
		} else {
			v.since = nil
		}
	}

	if int64(len(s.nodes)) <= s.minNodes {
		return TickResult{}, nil
	}

	var selected *nodeState
	for _, name := range nodeNames {
		v := s.nodes[name]
		if !removable[name] || v.since == nil || now-*v.since < s.t {
			continue
		}
		if selected == nil || *v.since < *selected.since || (*v.since == *selected.since && v.name < selected.name) {
			selected = v
		}
	}
	if selected == nil {
		return TickResult{}, nil
	}

	chosen := candidates[selected.name]
	result := TickResult{RemovedNode: selected.name}
	if len(chosen.migrations) != 0 {
		result.Migrations = append(result.Migrations, chosen.migrations...)
	}

	targetByPod := make(map[string]string, len(chosen.migrations))
	for _, migration := range chosen.migrations {
		targetByPod[migration.PodID] = migration.TargetNode
	}
	for _, migration := range chosen.migrations {
		pod := selected.pods[migration.PodID]
		targetName := targetByPod[pod.id]
		target := s.nodes[targetName]
		delete(selected.pods, pod.id)
		pod.node = target.name
		target.pods[pod.id] = pod
		selected.cpuUsed -= pod.cpu
		selected.memUsed -= pod.memory
		target.cpuUsed += pod.cpu
		target.memUsed += pod.memory
	}

	for podID := range selected.pods {
		delete(s.pods, podID)
	}
	delete(s.nodes, selected.name)
	return result, nil
}

func (s *Scaler) evaluateRemovable(
	v *nodeState,
	nodeNames []string,
	removable map[string]bool,
	simulatedCPU map[string]int64,
	simulatedMem map[string]int64,
	received map[string]bool,
) (candidate, bool) {
	var normalCPU int64
	var normalMem int64
	var normalPods []*podState
	for _, name := range v.pods {
		if name.kind == PodPinned {
			return candidate{}, false
		}
		if name.kind == PodNormal {
			normalCPU += name.cpu
			normalMem += name.memory
			normalPods = append(normalPods, name)
		}
	}
	if normalCPU*100 >= s.p*v.cpuAllocatable || normalMem*100 >= s.p*v.memAllocatable {
		return candidate{}, false
	}
	if received[v.name] {
		return candidate{}, false
	}

	sort.Slice(normalPods, func(i, j int) bool {
		left := normalPods[i]
		right := normalPods[j]
		if left.cpu != right.cpu {
			return left.cpu > right.cpu
		}
		if left.memory != right.memory {
			return left.memory > right.memory
		}
		return left.id < right.id
	})

	cand := candidate{}
	for _, pod := range normalPods {
		placed := false
		for _, targetName := range nodeNames {
			if targetName == v.name || removable[targetName] || received[targetName] {
				continue
			}
			target := s.nodes[targetName]
			cpuIncoming := simulatedCPU[targetName]
			memIncoming := simulatedMem[targetName]
			if target.cpuUsed+cpuIncoming+pod.cpu <= target.cpuAllocatable &&
				target.memUsed+memIncoming+pod.memory <= target.memAllocatable {
				simulatedCPU[targetName] += pod.cpu
				simulatedMem[targetName] += pod.memory
				cand.migrations = append(cand.migrations, PodMigration{PodID: pod.id, TargetNode: targetName})
				placed = true
				break
			}
		}
		if !placed {
			for _, migration := range cand.migrations {
				pod := s.pods[migration.PodID]
				simulatedCPU[migration.TargetNode] -= pod.cpu
				simulatedMem[migration.TargetNode] -= pod.memory
				if simulatedCPU[migration.TargetNode] == 0 {
					delete(simulatedCPU, migration.TargetNode)
				}
				if simulatedMem[migration.TargetNode] == 0 {
					delete(simulatedMem, migration.TargetNode)
				}
			}
			return candidate{}, false
		}
	}
	return cand, true
}
