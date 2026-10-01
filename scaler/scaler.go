package scaler

import "sync"

type PodKind string

const (
	PodNormal PodKind = "normal"
	PodDaemon PodKind = "daemon"
	PodPinned PodKind = "pinned"
)

type Node struct {
	Name           string
	CPUAllocatable int64
	MemAllocatable int64
}

type Pod struct {
	ID     string
	Node   string
	CPU    int64
	Memory int64
	Kind   PodKind
}

type PodMigration struct {
	PodID      string
	TargetNode string
}

type TickResult struct {
	RemovedNode string
	Migrations  []PodMigration
}

type nodeState struct {
	name           string
	cpuAllocatable int64
	memAllocatable int64
	cpuUsed        int64
	memUsed        int64
	pods           map[string]*podState
	since          *int64
}

type podState struct {
	id     string
	node   string
	cpu    int64
	memory int64
	kind   PodKind
}

type Scaler struct {
	mu                sync.RWMutex
	p                 int64
	t                 int64
	minNodes          int64
	lastNow           int64
	nodes             map[string]*nodeState
	pods              map[string]*podState
	lastTickRemovable map[string]bool
}

func New(thresholdPercent int64, durationMS int64, minNodes int64) (*Scaler, error) {
	if thresholdPercent < 1 || thresholdPercent > 100 || durationMS < 1 || minNodes < 0 {
		return nil, ErrInvalidConfig
	}
	return &Scaler{
		p:                 thresholdPercent,
		t:                 durationMS,
		minNodes:          minNodes,
		nodes:             make(map[string]*nodeState),
		pods:              make(map[string]*podState),
		lastTickRemovable: make(map[string]bool),
	}, nil
}

func (s *Scaler) AddNode(name string, cpuAllocatable int64, memAllocatable int64) error {
	if name == "" || cpuAllocatable < 1 || cpuAllocatable > 1e12 || memAllocatable < 1 || memAllocatable > 1e12 {
		return ErrInvalidNode
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.nodes[name]; exists {
		return ErrNodeExists
	}
	s.nodes[name] = &nodeState{
		name:           name,
		cpuAllocatable: cpuAllocatable,
		memAllocatable: memAllocatable,
		pods:           make(map[string]*podState),
	}
	return nil
}

func (s *Scaler) AddPod(pod Pod) error {
	if pod.ID == "" || !validPodKind(pod.Kind) || pod.CPU < 0 || pod.CPU > 1e12 || pod.Memory < 0 || pod.Memory > 1e12 || (pod.CPU == 0 && pod.Memory == 0) {
		return ErrInvalidPod
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.pods[pod.ID]; exists {
		return ErrPodExists
	}
	node, exists := s.nodes[pod.Node]
	if !exists {
		return ErrNodeNotFound
	}
	if node.cpuUsed+pod.CPU > node.cpuAllocatable || node.memUsed+pod.Memory > node.memAllocatable {
		return ErrInsufficientSpace
	}
	added := &podState{
		id:     pod.ID,
		node:   pod.Node,
		cpu:    pod.CPU,
		memory: pod.Memory,
		kind:   pod.Kind,
	}
	node.pods[pod.ID] = added
	s.pods[pod.ID] = added
	node.cpuUsed += pod.CPU
	node.memUsed += pod.Memory
	return nil
}

func (s *Scaler) RemovePod(podID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pod, exists := s.pods[podID]
	if !exists {
		return ErrPodNotFound
	}
	node := s.nodes[pod.node]
	node.cpuUsed -= pod.cpu
	node.memUsed -= pod.memory
	delete(node.pods, podID)
	delete(s.pods, podID)
	return nil
}

func validPodKind(kind PodKind) bool {
	return kind == PodNormal || kind == PodDaemon || kind == PodPinned
}
