package placement

import (
	"errors"
	"io"
	"log"
	"math/big"
	"sync"
)

var (
	ErrInvalidNodeCapacity     = errors.New("invalid node capacity")
	ErrDuplicateNodeID         = errors.New("duplicate node id")
	ErrInvalidReplicas         = errors.New("invalid replica count")
	ErrInvalidResourceRequest  = errors.New("invalid resource request")
	ErrInvalidReplicaLimit     = errors.New("invalid per-node replica limit")
	ErrDuplicateJob            = errors.New("duplicate job")
	ErrJobNotFound             = errors.New("job not found")
	ErrPermanentlyUnfit        = errors.New("job is permanently unfit")
	ErrTemporarilyInsufficient = errors.New("insufficient capacity for job at this time")
)

type NodeSpec struct {
	ID     string
	CPU    int64
	Memory int64
}

type JobSpec struct {
	ID               string
	Replicas         int64
	CPUPerReplica    int64
	MemoryPerReplica int64
	MaxPerNode       int64
}

type NodeStatus struct {
	ID         string
	CPU        int64
	Memory     int64
	UsedCPU    int64
	UsedMemory int64
}

type JobPlacement struct {
	JobID string
	Nodes []string
}

type nodeState struct {
	id         string
	cpu        int64
	memory     int64
	usedCPU    int64
	usedMemory int64
	replicas   map[string]int64
}

type placedJob struct {
	spec  JobSpec
	nodes []*nodeState
}

type Error struct {
	Op     string
	Reason error
	Detail string
}

func (e *Error) Error() string {
	return e.Op + ": " + e.Reason.Error()
}

func (e *Error) Unwrap() error {
	return e.Reason
}

type Placer struct {
	mu      sync.RWMutex
	nodes   []*nodeState
	nodeIDs map[string]*nodeState
	jobs    map[string]placedJob
	logger  *log.Logger
}

func NewPlacer(w io.Writer) *Placer {
	if w == nil {
		w = io.Discard
	}
	return &Placer{
		nodeIDs: make(map[string]*nodeState),
		jobs:    make(map[string]placedJob),
		logger:  log.New(w, "", log.LstdFlags),
	}
}

func (p *Placer) AddNodes(specs []NodeSpec) error {
	for _, spec := range specs {
		if spec.CPU <= 0 || spec.Memory <= 0 {
			err := &Error{Op: "add_nodes", Reason: ErrInvalidNodeCapacity, Detail: spec.ID}
			p.logger.Printf("AddNodes input=%v output=rejected reason=%v invalid_node=%s", specs, ErrInvalidNodeCapacity, spec.ID)
			return err
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if _, exists := seen[spec.ID]; exists {
			err := &Error{Op: "add_nodes", Reason: ErrDuplicateNodeID, Detail: spec.ID}
			p.logger.Printf("AddNodes input=%v output=rejected reason=%v duplicate_node=%s", specs, ErrDuplicateNodeID, spec.ID)
			return err
		}
		if _, exists := p.nodeIDs[spec.ID]; exists {
			err := &Error{Op: "add_nodes", Reason: ErrDuplicateNodeID, Detail: spec.ID}
			p.logger.Printf("AddNodes input=%v output=rejected reason=%v duplicate_node=%s", specs, ErrDuplicateNodeID, spec.ID)
			return err
		}
		seen[spec.ID] = struct{}{}
	}

	for _, spec := range specs {
		p.nodes = append(p.nodes, &nodeState{
			id:       spec.ID,
			cpu:      spec.CPU,
			memory:   spec.Memory,
			replicas: make(map[string]int64),
		})
		p.nodeIDs[spec.ID] = p.nodes[len(p.nodes)-1]
	}

	p.logger.Printf("AddNodes input=%v output=accepted added=%d", specs, len(specs))
	return nil
}

func (p *Placer) Place(job JobSpec) error {
	if job.Replicas < 1 {
		err := &Error{Op: "place", Reason: ErrInvalidReplicas, Detail: job.ID}
		p.logger.Printf("Place input=%v output=rejected reason=%v", job, ErrInvalidReplicas)
		return err
	}
	if job.CPUPerReplica <= 0 || job.MemoryPerReplica <= 0 {
		err := &Error{Op: "place", Reason: ErrInvalidResourceRequest, Detail: job.ID}
		p.logger.Printf("Place input=%v output=rejected reason=%v", job, ErrInvalidResourceRequest)
		return err
	}
	if job.MaxPerNode < 1 {
		err := &Error{Op: "place", Reason: ErrInvalidReplicaLimit, Detail: job.ID}
		p.logger.Printf("Place input=%v output=rejected reason=%v", job, ErrInvalidReplicaLimit)
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.jobs[job.ID]; exists {
		err := &Error{Op: "place", Reason: ErrDuplicateJob, Detail: job.ID}
		p.logger.Printf("Place input=%v output=rejected reason=%v", job, ErrDuplicateJob)
		return err
	}

	var fitEmpty int64
	for _, node := range p.nodes {
		if node.cpu >= job.CPUPerReplica && node.memory >= job.MemoryPerReplica {
			fitEmpty++
		}
	}
	replicaCeiling := new(big.Int).Mul(big.NewInt(job.MaxPerNode), big.NewInt(fitEmpty))
	if fitEmpty == 0 || replicaCeiling.Cmp(big.NewInt(job.Replicas)) < 0 {
		err := &Error{Op: "place", Reason: ErrPermanentlyUnfit, Detail: job.ID}
		p.logger.Printf("Place input=%v output=rejected reason=%v fit_empty=%d replicas=%d max_per_node=%d basis=empty_capacity_or_replica_ceiling",
			job, ErrPermanentlyUnfit, fitEmpty, job.Replicas, job.MaxPerNode)
		return err
	}

	selected := make([]*nodeState, 0, job.Replicas)
	for replica := int64(0); replica < job.Replicas; replica++ {
		var best *nodeState
		var bestCPULeft int64
		var bestMemoryLeft int64
		var bestScore fraction

		for _, node := range p.nodes {
			if node.replicas[job.ID] >= job.MaxPerNode {
				continue
			}
			cpuLeft := node.cpu - node.usedCPU - job.CPUPerReplica
			memoryLeft := node.memory - node.usedMemory - job.MemoryPerReplica
			if cpuLeft < 0 || memoryLeft < 0 {
				continue
			}

			score := maxFraction(
				fraction{numerator: cpuLeft, denominator: node.cpu},
				fraction{numerator: memoryLeft, denominator: node.memory},
			)
			if best == nil || fractionLess(score, bestScore) || (fractionEqual(score, bestScore) && node.id < best.id) {
				best = node
				bestCPULeft = cpuLeft
				bestMemoryLeft = memoryLeft
				bestScore = score
			}
		}

		if best == nil {
			for index := len(selected) - 1; index >= 0; index-- {
				selectedNode := selected[index]
				selectedNode.usedCPU -= job.CPUPerReplica
				selectedNode.usedMemory -= job.MemoryPerReplica
				selectedNode.replicas[job.ID]--
				if selectedNode.replicas[job.ID] == 0 {
					delete(selectedNode.replicas, job.ID)
				}
			}
			err := &Error{Op: "place", Reason: ErrTemporarilyInsufficient, Detail: job.ID}
			p.logger.Printf("Place input=%v output=rejected reason=%v failed_replica=%d selected_before_failure=%d basis=greedy_has_no_candidate_after_prior_replicas",
				job, ErrTemporarilyInsufficient, replica, len(selected))
			return err
		}

		best.usedCPU += job.CPUPerReplica
		best.usedMemory += job.MemoryPerReplica
		best.replicas[job.ID]++
		selected = append(selected, best)
		p.logger.Printf("Place input=%v replica=%d selected_node=%s cpu_left=%d/%d memory_left=%d/%d basis=largest_remaining_fraction_then_id",
			job, replica, best.id, bestCPULeft, best.cpu, bestMemoryLeft, best.memory)
	}

	nodeIDs := make([]string, 0, len(selected))
	for _, node := range selected {
		nodeIDs = append(nodeIDs, node.id)
	}
	p.jobs[job.ID] = placedJob{spec: job, nodes: selected}
	p.logger.Printf("Place input=%v output=accepted placement=%v", job, nodeIDs)
	return nil
}

type fraction struct {
	numerator   int64
	denominator int64
}

func maxFraction(left, right fraction) fraction {
	if fractionLess(left, right) {
		return right
	}
	return left
}

func fractionLess(left, right fraction) bool {
	return compareFractions(left, right) < 0
}

func fractionEqual(left, right fraction) bool {
	return compareFractions(left, right) == 0
}

func compareFractions(left, right fraction) int {
	lhs := new(big.Int).Mul(big.NewInt(left.numerator), big.NewInt(right.denominator))
	rhs := new(big.Int).Mul(big.NewInt(right.numerator), big.NewInt(left.denominator))
	return lhs.Cmp(rhs)
}

func (p *Placer) Remove(jobID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	job, exists := p.jobs[jobID]
	if !exists {
		err := &Error{Op: "remove", Reason: ErrJobNotFound, Detail: jobID}
		p.logger.Printf("Remove input=%q output=rejected reason=%v", jobID, ErrJobNotFound)
		return err
	}

	for _, node := range job.nodes {
		node.usedCPU -= job.spec.CPUPerReplica
		node.usedMemory -= job.spec.MemoryPerReplica
		node.replicas[jobID]--
		if node.replicas[jobID] == 0 {
			delete(node.replicas, jobID)
		}
	}
	delete(p.jobs, jobID)

	nodeIDs := make([]string, 0, len(job.nodes))
	for _, node := range job.nodes {
		nodeIDs = append(nodeIDs, node.id)
	}
	p.logger.Printf("Remove input=%q output=accepted released=%v", jobID, nodeIDs)
	return nil
}

func (p *Placer) JobPlacement(jobID string) (JobPlacement, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	job, exists := p.jobs[jobID]
	if !exists {
		err := &Error{Op: "query", Reason: ErrJobNotFound, Detail: jobID}
		p.logger.Printf("JobPlacement input=%q output=rejected reason=%v", jobID, ErrJobNotFound)
		return JobPlacement{}, err
	}

	nodeIDs := make([]string, len(job.nodes))
	for index, node := range job.nodes {
		nodeIDs[index] = node.id
	}
	placement := JobPlacement{JobID: jobID, Nodes: nodeIDs}
	p.logger.Printf("JobPlacement input=%q output=%v", jobID, nodeIDs)
	return placement, nil
}

func (p *Placer) Nodes() []NodeStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()

	nodes := make([]NodeStatus, len(p.nodes))
	for index, node := range p.nodes {
		nodes[index] = NodeStatus{
			ID:         node.id,
			CPU:        node.cpu,
			Memory:     node.memory,
			UsedCPU:    node.usedCPU,
			UsedMemory: node.usedMemory,
		}
	}
	p.logger.Printf("Nodes output=%v", nodes)
	return nodes
}
