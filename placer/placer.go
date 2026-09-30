// Package placer implements an all-or-nothing multi-dimensional gang
// bin-packing placement algorithm.
package placer

import (
	"errors"
	"io"
	"log"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Node describes a physical node identified by ID with two resource
// dimensions (CPU and memory).
type Node struct {
	ID     string
	CPU    int64
	Memory int64
}

// Job describes a gang workload: k identical replicas that must either
// all be placed or none of them.
type Job struct {
	ID         string
	Replicas   int
	CPU        int64
	Memory     int64
	MaxPerNode int
}

// Reasons returned by rejected operations.
var (
	ErrNodeCapacityNonPositive = errors.New("node capacity must be positive in both dimensions")
	ErrDuplicateNodeID         = errors.New("duplicate node id")
	ErrReplicasTooSmall        = errors.New("job replicas must be >= 1")
	ErrRequestNonPositive      = errors.New("replica request must be positive in both dimensions")
	ErrMaxPerNodeTooSmall      = errors.New("job max-per-node must be >= 1")
	ErrDuplicateJob            = errors.New("job already placed")
	ErrJobNotFound             = errors.New("job not found")
	ErrPermanentlyInfeasible   = errors.New("job permanently infeasible: no node can host a replica even when empty, or replicas exceed max-per-node capacity")
	ErrTemporarilyInsufficient = errors.New("temporarily insufficient capacity: greedy placement has no candidate for a replica")
)

type nodeState struct {
	id      string
	cpu     int64
	memory  int64
	cpuUsed int64
	memUsed int64
}

type jobState struct {
	job       Job
	nodes     []string // node id of each replica, in replica order
	nodeCount map[string]int
}

// Placer is the concurrency-safe gang placement engine.
type Placer struct {
	mu    sync.RWMutex
	nodes map[string]*nodeState
	order []string // insertion order, keeps iteration deterministic
	jobs  map[string]*jobState
	log   *log.Logger
}

// NewPlacer creates a placer initialized with the given nodes.
// Construction fails wholesale on invalid node parameters; in that case
// the returned placer is nil and no partial state is observable.
func NewPlacer(nodes []Node) (*Placer, error) {
	for _, n := range nodes {
		if n.CPU <= 0 || n.Memory <= 0 {
			return nil, ErrNodeCapacityNonPositive
		}
	}

	seen := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if seen[n.ID] {
			return nil, ErrDuplicateNodeID
		}
		seen[n.ID] = true
	}

	p := &Placer{
		nodes: make(map[string]*nodeState, len(nodes)),
		order: make([]string, 0, len(nodes)),
		jobs:  make(map[string]*jobState),
		log:   log.New(os.Stderr, "[placer] ", log.LstdFlags|log.Lmicroseconds),
	}
	for _, n := range nodes {
		p.nodes[n.ID] = &nodeState{id: n.ID, cpu: n.CPU, memory: n.Memory}
		p.order = append(p.order, n.ID)
	}
	p.log.Printf("init nodes=%s", formatNodes(nodes))
	return p, nil
}

// SetLogger redirects the decision log. Passing nil disables logging.
func (p *Placer) SetLogger(w io.Writer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w == nil {
		p.log = log.New(io.Discard, "", 0)
	} else {
		p.log = log.New(w, "[placer] ", log.LstdFlags|log.Lmicroseconds)
	}
}

// validateJob checks job parameters in the required order.
func validateJob(job Job) error {
	if job.Replicas < 1 {
		return ErrReplicasTooSmall
	}
	if job.CPU <= 0 || job.Memory <= 0 {
		return ErrRequestNonPositive
	}
	if job.MaxPerNode < 1 {
		return ErrMaxPerNodeTooSmall
	}
	return nil
}

// Place either places every replica of the job on nodes, or rejects the
// whole operation without changing any node occupancy.
//
// Replicas are placed one by one in replica order. For each replica the
// candidate set is every node whose remaining CPU and memory both fit the
// request and which currently hosts fewer than job.MaxPerNode replicas of
// this job. A candidate's score is max(remainingCPU/totalCPU,
// remainingMemory/totalMemory) measured after placing the replica; the
// smallest score wins, ties broken by ascending node ID. Scores are
// compared with exact cross multiplication, never floating point.
func (p *Placer) Place(job Job) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := validateJob(job); err != nil {
		p.log.Printf("place job=%s input={k=%d cpu=%d mem=%d a=%d} REJECT reason=%q",
			job.ID, job.Replicas, job.CPU, job.Memory, job.MaxPerNode, err.Error())
		return err
	}
	if _, exists := p.jobs[job.ID]; exists {
		p.log.Printf("place job=%s REJECT reason=%q", job.ID, ErrDuplicateJob.Error())
		return ErrDuplicateJob
	}

	p.log.Printf("place job=%s input={k=%d cpu=%d mem=%d a=%d} begin",
		job.ID, job.Replicas, job.CPU, job.Memory, job.MaxPerNode)

	// Tentative placements, applied directly and rolled back on failure.
	chosen := make([]string, 0, job.Replicas)
	counts := make(map[string]int)

	for r := 0; r < job.Replicas; r++ {
		node, err := p.pickNode(job, r, counts)
		if err != nil {
			// Roll back every replica chosen so far.
			for i, id := range chosen {
				n := p.nodes[id]
				n.cpuUsed -= job.CPU
				n.memUsed -= job.Memory
				p.log.Printf("rollback job=%s replica=%d node=%s usage-after=(%d/%d cpu, %d/%d mem)",
					job.ID, i, id, n.cpuUsed, n.cpu, n.memUsed, n.memory)
			}

			reason := p.classify(job)
			p.log.Printf("place job=%s REJECT reason=%q at replica=%d (tentative choices rolled back: %s)",
				job.ID, reason.Error(), r, joinIDs(chosen))
			return reason
		}

		n := p.nodes[node]
		n.cpuUsed += job.CPU
		n.memUsed += job.Memory
		counts[node]++
		chosen = append(chosen, node)
		p.log.Printf("pick job=%s replica=%d node=%s remaining-after=(%d/%d cpu, %d/%d mem)",
			job.ID, r, node, n.cpu-n.cpuUsed, n.cpu, n.memory-n.memUsed, n.memory)
	}

	p.jobs[job.ID] = &jobState{
		job:       job,
		nodes:     chosen,
		nodeCount: counts,
	}
	p.log.Printf("place job=%s OK output=%s", job.ID, joinIDs(chosen))
	return nil
}

// pickNode runs the exact (integer cross multiplication) scoring step for
// one replica, considering the tentative counts of replicas already
// chosen for this job during the current Place call.
func (p *Placer) pickNode(job Job, replica int, tentative map[string]int) (string, error) {
	var best string
	var bestCPU, bestMem, bestTotalCPU, bestTotalMem int64 // post-placement remaining ratio: bestCPU/bestTotalCPU, bestMem/bestTotalMem
	found := false

	for _, id := range p.order {
		n := p.nodes[id]
		if tentative[id] >= job.MaxPerNode {
			continue
		}
		remCPU := n.cpu - n.cpuUsed
		remMem := n.memory - n.memUsed
		if remCPU < job.CPU || remMem < job.Memory {
			continue
		}

		// Remaining ratio components after this replica is placed.
		cpuAfter := remCPU - job.CPU
		memAfter := remMem - job.Memory

		if !found {
			best, found = id, true
			bestCPU, bestMem, bestTotalCPU, bestTotalMem = cpuAfter, memAfter, n.cpu, n.memory
			continue
		}

		if lessScore(cpuAfter, memAfter, n.cpu, n.memory,
			bestCPU, bestMem, bestTotalCPU, bestTotalMem, id, best) {
			best = id
			bestCPU, bestMem, bestTotalCPU, bestTotalMem = cpuAfter, memAfter, n.cpu, n.memory
		}
	}

	if !found {
		return "", ErrTemporarilyInsufficient
	}
	return best, nil
}

// lessScore reports whether candidate (cpuAfter/totalCPU, memAfter/
// totalMem) beats the incumbent under max-of-two-ratios scoring. Ratios
// are compared exactly via cross multiplication using arbitrary-precision
// integers; equal scores break by ascending node ID.
func lessScore(cpuA, memA, totalCPUA, totalMemA int64,
	cpuB, memB, totalCPUB, totalMemB int64, idA, idB string) bool {

	maxA := maxRatio(cpuA, totalCPUA, memA, totalMemA)
	maxB := maxRatio(cpuB, totalCPUB, memB, totalMemB)

	cmp := crossCompare(maxA.num, maxA.den, maxB.num, maxB.den)
	if cmp != 0 {
		return cmp < 0
	}
	return idA < idB
}

type frac struct{ num, den int64 }

func maxRatio(cpu, totalCPU, mem, totalMem int64) frac {
	if crossCompare(cpu, totalCPU, mem, totalMem) >= 0 {
		return frac{cpu, totalCPU}
	}
	return frac{mem, totalMem}
}

// crossCompare compares a/b and c/d exactly without floating point.
func crossCompare(a, b, c, d int64) int {
	lhs := new(big.Int).Mul(big.NewInt(a), big.NewInt(d))
	rhs := new(big.Int).Mul(big.NewInt(c), big.NewInt(b))
	return lhs.Cmp(rhs)
}

// classify decides permanent infeasibility vs temporary shortage for a
// job that the greedy algorithm could not place. n is the number of
// nodes able to hold a single replica of the job on an empty node; the
// job is permanently infeasible when n == 0 or k > a*n.
func (p *Placer) classify(job Job) error {
	n := 0
	for _, id := range p.order {
		node := p.nodes[id]
		if node.cpu >= job.CPU && node.memory >= job.Memory {
			n++
		}
	}
	if n == 0 || job.Replicas > job.MaxPerNode*n {
		p.log.Printf("classify job=%s permanent n=%d k=%d a=%d (a*n=%d)",
			job.ID, n, job.Replicas, job.MaxPerNode, job.MaxPerNode*n)
		return ErrPermanentlyInfeasible
	}
	p.log.Printf("classify job=%s temporary n=%d k=%d a=%d (a*n=%d)",
		job.ID, n, job.Replicas, job.MaxPerNode, job.MaxPerNode*n)
	return ErrTemporarilyInsufficient
}

// Remove releases all replicas of a previously placed job, restoring
// every node to its pre-placement occupancy.
func (p *Placer) Remove(jobID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	js, ok := p.jobs[jobID]
	if !ok {
		p.log.Printf("remove job=%s REJECT reason=%q", jobID, ErrJobNotFound.Error())
		return ErrJobNotFound
	}

	for i, id := range js.nodes {
		n := p.nodes[id]
		n.cpuUsed -= js.job.CPU
		n.memUsed -= js.job.Memory
		p.log.Printf("release job=%s replica=%d node=%s remaining=(%d/%d cpu, %d/%d mem)",
			jobID, i, id, n.cpu-n.cpuUsed, n.cpu, n.memory-n.memUsed, n.memory)
	}
	delete(p.jobs, jobID)
	p.log.Printf("remove job=%s OK output=released %d replicas", jobID, len(js.nodes))
	return nil
}

// JobNodes returns the node hosting each replica, in replica order.
func (p *Placer) JobNodes(jobID string) ([]string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	js, ok := p.jobs[jobID]
	if !ok {
		return nil, false
	}
	out := make([]string, len(js.nodes))
	copy(out, js.nodes)
	return out, true
}

// NodeUsage reports the current CPU and memory usage of a node.
func (p *Placer) NodeUsage(nodeID string) (cpu, memory int64, ok bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	n, exists := p.nodes[nodeID]
	if !exists {
		return 0, 0, false
	}
	return n.cpuUsed, n.memUsed, true
}

// Snapshot returns a deterministic copy of the current placement:
// node id -> job id -> replica count on that node, with node ids sorted.
func (p *Placer) Snapshot() map[string]map[string]int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make(map[string]map[string]int, len(p.order))
	for _, id := range p.order {
		out[id] = map[string]int{}
	}
	for jid, js := range p.jobs {
		for nid, c := range js.nodeCount {
			out[nid][jid] = c
		}
	}
	return out
}

func formatNodes(nodes []Node) string {
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = n.ID + "(" + itoa(n.CPU) + " cpu," + itoa(n.Memory) + " mem)"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func joinIDs(ids []string) string {
	return "[" + strings.Join(ids, ",") + "]"
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
