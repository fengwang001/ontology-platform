package scaler

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type naiveNode struct {
	capCPU int64
	capMem int64
	pods   map[string]*naivePod
	since  *int64
}

type naivePod struct {
	id   string
	node string
	cpu  int64
	mem  int64
	kind PodKind
}

type naiveWorld struct {
	p        int64
	t        int64
	minNodes int64
	nodes    map[string]*naiveNode
	pods     map[string]*naivePod
	lastNow  int64
	logs     []string
}

func newNaiveWorld(p, t, minNodes int64) *naiveWorld {
	return &naiveWorld{p: p, t: t, minNodes: minNodes, nodes: make(map[string]*naiveNode), pods: make(map[string]*naivePod)}
}

func (w *naiveWorld) addNode(name string, cpu, mem int64) error {
	w.logs = append(w.logs, fmt.Sprintf("AddNode name=%s cpu=%d mem=%d", name, cpu, mem))
	if name == "" || cpu < 1 || cpu > 1e12 || mem < 1 || mem > 1e12 {
		w.logs = append(w.logs, "AddNode rejected: invalid node")
		return ErrInvalidNode
	}
	if _, ok := w.nodes[name]; ok {
		w.logs = append(w.logs, "AddNode rejected: duplicate")
		return ErrNodeExists
	}
	w.nodes[name] = &naiveNode{capCPU: cpu, capMem: mem, pods: make(map[string]*naivePod)}
	w.logs = append(w.logs, "AddNode accepted")
	return nil
}

func (w *naiveWorld) addPod(p Pod) error {
	w.logs = append(w.logs, fmt.Sprintf("AddPod id=%s node=%s cpu=%d mem=%d kind=%s", p.ID, p.Node, p.CPU, p.Memory, p.Kind))
	if p.ID == "" || !validPodKind(p.Kind) || p.CPU < 0 || p.CPU > 1e12 || p.Memory < 0 || p.Memory > 1e12 || (p.CPU == 0 && p.Memory == 0) {
		w.logs = append(w.logs, "AddPod rejected: invalid pod")
		return ErrInvalidPod
	}
	if _, ok := w.pods[p.ID]; ok {
		w.logs = append(w.logs, "AddPod rejected: duplicate")
		return ErrPodExists
	}
	node, ok := w.nodes[p.Node]
	if !ok {
		w.logs = append(w.logs, "AddPod rejected: node missing")
		return ErrNodeNotFound
	}
	usedCPU, usedMem := w.usage(node)
	if usedCPU+p.CPU > node.capCPU || usedMem+p.Memory > node.capMem {
		w.logs = append(w.logs, "AddPod rejected: capacity")
		return ErrInsufficientSpace
	}
	added := &naivePod{id: p.ID, node: p.Node, cpu: p.CPU, mem: p.Memory, kind: p.Kind}
	node.pods[p.ID] = added
	w.pods[p.ID] = added
	w.logs = append(w.logs, "AddPod accepted")
	return nil
}

func (w *naiveWorld) removePod(id string) error {
	w.logs = append(w.logs, "RemovePod id="+id)
	pod, ok := w.pods[id]
	if !ok {
		w.logs = append(w.logs, "RemovePod rejected: missing")
		return ErrPodNotFound
	}
	delete(w.nodes[pod.node].pods, id)
	delete(w.pods, id)
	w.logs = append(w.logs, "RemovePod accepted")
	return nil
}

func (w *naiveWorld) usage(node *naiveNode) (int64, int64) {
	var cpu, mem int64
	for _, pod := range node.pods {
		cpu += pod.cpu
		mem += pod.mem
	}
	return cpu, mem
}

func (w *naiveWorld) normalUsage(node *naiveNode) (int64, int64) {
	var cpu, mem int64
	for _, pod := range node.pods {
		if pod.kind == PodNormal {
			cpu += pod.cpu
			mem += pod.mem
		}
	}
	return cpu, mem
}

func sortedNames(nodes map[string]*naiveNode) []string {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedNormalPods(node *naiveNode) []*naivePod {
	var pods []*naivePod
	for _, pod := range node.pods {
		if pod.kind == PodNormal {
			pods = append(pods, pod)
		}
	}
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].cpu != pods[j].cpu {
			return pods[i].cpu > pods[j].cpu
		}
		if pods[i].mem != pods[j].mem {
			return pods[i].mem > pods[j].mem
		}
		return pods[i].id < pods[j].id
	})
	return pods
}

func (w *naiveWorld) tick(now int64) (TickResult, error) {
	w.logs = append(w.logs, fmt.Sprintf("Tick now=%d", now))
	if now < 0 {
		w.logs = append(w.logs, "Tick rejected: invalid time")
		return TickResult{}, ErrInvalidTime
	}
	if now < w.lastNow {
		w.logs = append(w.logs, "Tick rejected: rollback")
		return TickResult{}, ErrClockRollback
	}
	w.lastNow = now
	names := sortedNames(w.nodes)
	removable := make(map[string]bool)
	received := make(map[string]bool)
	incomingCPU := make(map[string]int64)
	incomingMem := make(map[string]int64)
	placementsByNode := make(map[string]map[string]string)
	normalByNode := make(map[string][]*naivePod)

	for _, name := range names {
		node := w.nodes[name]
		normalCPU, normalMem := w.normalUsage(node)
		low := normalCPU*100 < w.p*node.capCPU && normalMem*100 < w.p*node.capMem
		hasPinned := false
		for _, pod := range node.pods {
			if pod.kind == PodPinned {
				hasPinned = true
			}
		}
		ok := low && !received[name] && !hasPinned
		placements := make(map[string]string)
		tentativeCPU := map[string]int64{}
		tentativeMem := map[string]int64{}
		var normal []*naivePod
		if ok {
			normal = sortedNormalPods(node)
			for _, pod := range normal {
				placed := false
				for _, targetName := range names {
					if targetName == name || removable[targetName] || received[targetName] {
						continue
					}
					target := w.nodes[targetName]
					usedCPU, usedMem := w.usage(target)
					if usedCPU+incomingCPU[targetName]+tentativeCPU[targetName]+pod.cpu <= target.capCPU &&
						usedMem+incomingMem[targetName]+tentativeMem[targetName]+pod.mem <= target.capMem {
						tentativeCPU[targetName] += pod.cpu
						tentativeMem[targetName] += pod.mem
						placements[pod.id] = targetName
						placed = true
						w.logs = append(w.logs, fmt.Sprintf("candidate=%s temporary pod=%s target=%s", name, pod.id, targetName))
						break
					}
				}
				if !placed {
					ok = false
					w.logs = append(w.logs, fmt.Sprintf("candidate=%s failed at pod=%s; temporary placements rolled back", name, pod.id))
					break
				}
			}
		}
		if ok {
			removable[name] = true
			if node.since == nil {
				value := now
				node.since = &value
			}
			placementsByNode[name] = placements
			normalByNode[name] = normal
			for _, targetName := range placements {
				received[targetName] = true
			}
			for targetName, value := range tentativeCPU {
				incomingCPU[targetName] += value
			}
			for targetName, value := range tentativeMem {
				incomingMem[targetName] += value
			}
			w.logs = append(w.logs, fmt.Sprintf("candidate=%s removable=true since=%d", name, *node.since))
		} else {
			node.since = nil
			w.logs = append(w.logs, fmt.Sprintf("candidate=%s removable=false low=%v received=%v pinned=%v", name, low, received[name], hasPinned))
		}
	}

	result := TickResult{}
	if int64(len(w.nodes)) <= w.minNodes {
		w.logs = append(w.logs, "no removal: min nodes reached")
		return result, nil
	}
	var chosen string
	for _, name := range names {
		node := w.nodes[name]
		if !removable[name] || node.since == nil || now-*node.since < w.t {
			continue
		}
		if chosen == "" || *node.since < *w.nodes[chosen].since || (*node.since == *w.nodes[chosen].since && name < chosen) {
			chosen = name
		}
	}
	if chosen == "" {
		w.logs = append(w.logs, "no removal: duration not reached")
		return result, nil
	}
	from := w.nodes[chosen]
	for _, pod := range normalByNode[chosen] {
		targetName := placementsByNode[chosen][pod.id]
		target := w.nodes[targetName]
		delete(from.pods, pod.id)
		pod.node = targetName
		target.pods[pod.id] = pod
		result.Migrations = append(result.Migrations, PodMigration{PodID: pod.id, TargetNode: targetName})
	}
	for id := range from.pods {
		delete(w.pods, id)
	}
	delete(w.nodes, chosen)
	result.RemovedNode = chosen
	w.logs = append(w.logs, fmt.Sprintf("removed=%s migrations=%d", chosen, len(result.Migrations)))
	return result, nil
}

func TestRandomScenariosMatchNaiveSimulation(t *testing.T) {
	if !testing.Verbose() {
		t.Log("rerun with -v for each random scenario input, output, and decision log")
	}
	rng := rand.New(rand.NewSource(20261002))
	for scenario := 0; scenario < 2000; scenario++ {
		p := int64(rng.Intn(100) + 1)
		duration := int64(rng.Intn(5) + 1)
		minNodes := int64(rng.Intn(4))
		actual, err := New(p, duration, minNodes)
		if err != nil {
			t.Fatal(err)
		}
		naive := newNaiveWorld(p, duration, minNodes)
		nodeCount := rng.Intn(6)
		podSeq := 0
		validNodeNames := make([]string, 0)
		for i := 0; i < nodeCount; i++ {
			name := fmt.Sprintf("n%02d", i)
			capCPU := int64(rng.Intn(15) + 1)
			capMem := int64(rng.Intn(15) + 1)
			if rng.Intn(10) == 0 {
				name = ""
			}
			errA := actual.AddNode(name, capCPU, capMem)
			errN := naive.addNode(name, capCPU, capMem)
			if !sameSentinel(errA, errN) {
				t.Fatalf("scenario %d AddNode errors actual=%v naive=%v", scenario, errA, errN)
			}
			assertWorldsMatch(t, scenario, actual, naive)
			validNodeNames = sortedNames(naive.nodes)
		}
		for op := 0; op < 24; op++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3:
				if len(validNodeNames) == 0 {
					continue
				}
				name := validNodeNames[rng.Intn(len(validNodeNames))]
				node := naive.nodes[name]
				usedCPU, usedMem := naive.usage(node)
				maxCPU := node.capCPU - usedCPU
				maxMem := node.capMem - usedMem
				cpu := int64(rng.Intn(int(maxCPU + 4)))
				mem := int64(rng.Intn(int(maxMem + 4)))
				if rng.Intn(12) == 0 {
					cpu, mem = 0, 0
				}
				kinds := []PodKind{PodNormal, PodNormal, PodNormal, PodDaemon, PodPinned}
				kind := kinds[rng.Intn(len(kinds))]
				id := fmt.Sprintf("p%03d", podSeq)
				podSeq++
				if rng.Intn(8) == 0 {
					id = ""
				}
				pod := Pod{ID: id, Node: name, CPU: cpu, Memory: mem, Kind: kind}
				errA := actual.AddPod(pod)
				errN := naive.addPod(pod)
				if !sameSentinel(errA, errN) {
					t.Fatalf("scenario %d AddPod errors actual=%v naive=%v\n%s", scenario, errA, errN, strings.Join(naive.logs, "\n"))
				}
				assertWorldsMatch(t, scenario, actual, naive)
			case 4:
				if len(naive.pods) == 0 {
					continue
				}
				ids := make([]string, 0, len(naive.pods))
				for id := range naive.pods {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				id := ids[rng.Intn(len(ids))]
				errA := actual.RemovePod(id)
				errN := naive.removePod(id)
				if !sameSentinel(errA, errN) {
					t.Fatalf("scenario %d RemovePod errors actual=%v naive=%v", scenario, errA, errN)
				}
				assertWorldsMatch(t, scenario, actual, naive)
			default:
				now := int64(rng.Intn(13))
				if rng.Intn(8) == 0 {
					now = -1
				}
				got, errA := actual.Tick(now)
				want, errN := naive.tick(now)
				validNodeNames = sortedNames(naive.nodes)
				if !sameSentinel(errA, errN) || got.RemovedNode != want.RemovedNode || !migrationsEqual(got.Migrations, want.Migrations) {
					t.Fatalf("scenario %d Tick(%d) actual=%+v,%v want=%+v,%v\n%s", scenario, now, got, errA, want, errN, strings.Join(naive.logs, "\n"))
				}
				assertWorldsMatch(t, scenario, actual, naive)
			}
		}
		if testing.Verbose() {
			t.Logf("scenario=%d p=%d t=%d min=%d\n%s", scenario, p, duration, minNodes, strings.Join(naive.logs, "\n"))
		}
	}
}

func sameSentinel(left, right error) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Error() == right.Error()
}

func migrationsEqual(left, right []PodMigration) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func assertWorldsMatch(t *testing.T, scenario int, actual *Scaler, naive *naiveWorld) {
	t.Helper()
	actual.mu.RLock()
	defer actual.mu.RUnlock()
	if len(actual.nodes) != len(naive.nodes) || len(actual.pods) != len(naive.pods) {
		t.Fatalf("scenario %d size mismatch: actual nodes=%d pods=%d naive nodes=%d pods=%d\n%s", scenario, len(actual.nodes), len(actual.pods), len(naive.nodes), len(naive.pods), strings.Join(naive.logs, "\n"))
	}
	for name, actualNode := range actual.nodes {
		naiveNode, ok := naive.nodes[name]
		if !ok {
			t.Fatalf("scenario %d unexpected actual node %s", scenario, name)
		}
		if actualNode.cpuAllocatable != naiveNode.capCPU || actualNode.memAllocatable != naiveNode.capMem {
			t.Fatalf("scenario %d node %s capacity actual=(%d,%d) naive=(%d,%d)", scenario, name, actualNode.cpuAllocatable, actualNode.memAllocatable, naiveNode.capCPU, naiveNode.capMem)
		}
		actualCPU, actualMem := actual.usageLocked(actualNode)
		naiveCPU, naiveMem := naive.usage(naiveNode)
		if actualCPU != naiveCPU || actualMem != naiveMem || actualCPU != actualNode.cpuUsed || actualMem != actualNode.memUsed {
			t.Fatalf("scenario %d node %s usage actual sum=(%d,%d) tracked=(%d,%d) naive=(%d,%d)", scenario, name, actualCPU, actualMem, actualNode.cpuUsed, actualNode.memUsed, naiveCPU, naiveMem)
		}
		if !sinceMatches(actualNode.since, naiveNode.since) {
			t.Fatalf("scenario %d node %s since actual=%s naive=%s", scenario, name, sinceText(actualNode.since), sinceText(naiveNode.since))
		}
		if len(actualNode.pods) != len(naiveNode.pods) {
			t.Fatalf("scenario %d node %s pod count actual=%d naive=%d", scenario, name, len(actualNode.pods), len(naiveNode.pods))
		}
		for id, actualPod := range actualNode.pods {
			naivePod, ok := naiveNode.pods[id]
			if !ok || actualPod.id != naivePod.id || actualPod.node != naivePod.node || actualPod.cpu != naivePod.cpu || actualPod.memory != naivePod.mem || actualPod.kind != naivePod.kind {
				t.Fatalf("scenario %d pod %s mismatch actual=%+v naive=%+v", scenario, id, actualPod, naivePod)
			}
		}
	}
	for id := range naive.pods {
		if _, ok := actual.pods[id]; !ok {
			t.Fatalf("scenario %d missing actual pod %s", scenario, id)
		}
	}
}

func (s *Scaler) usageLocked(node *nodeState) (int64, int64) {
	var cpu, mem int64
	for _, pod := range node.pods {
		cpu += pod.cpu
		mem += pod.memory
	}
	return cpu, mem
}

func sinceMatches(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func sinceText(value *int64) string {
	if value == nil {
		return "absent"
	}
	return fmt.Sprintf("%d", *value)
}
