package cpm

import (
	"container/heap"
	"errors"
	"sync"
)

var (
	ErrInvalidParameter   = errors.New("cpm: invalid parameter")
	ErrTaskNotFound       = errors.New("cpm: task not found")
	ErrDependencyExists   = errors.New("cpm: dependency already exists")
	ErrCapacityExceeded   = errors.New("cpm: capacity exceeded")
	ErrDependencyNotFound = errors.New("cpm: dependency not found")
	ErrCycle              = errors.New("cpm: dependency would create a cycle")
	ErrNoBaseline         = errors.New("cpm: no baseline")
)

type UpdateReport struct {
	ChangedES   []int
	ChangedLF   []int
	OldPF       int64
	NewPF       int64
	CritAdded   []int
	CritRemoved []int
}

type edgeState struct {
	fwd         *candidate
	bwd         *candidate
	fwdVersions []*candidate
	bwdVersions []*candidate
	lag         int64
	from        int
	to          int
}

type edgeView struct {
	From int
	To   int
	Lag  int64
}

type Maintainer struct {
	maxTasks int
	maxDeps  int
	deadline int64

	mu sync.RWMutex

	taskCount int
	depCount  int

	duration []int64
	es       []int64
	ef       []int64
	ls       []int64
	lf       []int64
	tf       []int64
	snet     []int64
	fnlt     []int64

	inCandidates  [][]*candidate
	outCandidates [][]*candidate
	inHeaps       []maxHeap
	outHeaps      []minHeap
	outEdges      [][]*edgeState
	inEdges       [][]*edgeState

	edges map[[2]int]*edgeState

	pfItems []*valueItem
	pfHeap  valueMaxHeap
	tfItems []*valueItem
	tfHeap  valueMinHeap

	tfBuckets    map[int64]map[int]struct{}
	critical     []bool
	criticalList []int

	baseline      []int64
	baselineTasks int
	hasBaseline   bool

	fwdEval int
	bwdEval int
}

func NewMaintainer(maxTasks, maxDeps int, deadline int64) (*Maintainer, error) {
	if maxTasks < 1 || maxTasks > 100000 || maxDeps < 1 || maxDeps > 500000 || deadline < 0 || deadline > 1_000_000_000_000 {
		return nil, ErrInvalidParameter
	}
	return &Maintainer{
		maxTasks:      maxTasks,
		maxDeps:       maxDeps,
		deadline:      deadline,
		duration:      make([]int64, maxTasks),
		es:            make([]int64, maxTasks),
		ef:            make([]int64, maxTasks),
		ls:            make([]int64, maxTasks),
		lf:            make([]int64, maxTasks),
		tf:            make([]int64, maxTasks),
		snet:          make([]int64, maxTasks),
		fnlt:          make([]int64, maxTasks),
		inCandidates:  make([][]*candidate, maxTasks),
		outCandidates: make([][]*candidate, maxTasks),
		inHeaps:       make([]maxHeap, maxTasks),
		outHeaps:      make([]minHeap, maxTasks),
		outEdges:      make([][]*edgeState, maxTasks),
		inEdges:       make([][]*edgeState, maxTasks),
		edges:         make(map[[2]int]*edgeState, maxDeps),
		pfItems:       make([]*valueItem, maxTasks),
		tfItems:       make([]*valueItem, maxTasks),
		tfBuckets:     make(map[int64]map[int]struct{}),
		critical:      make([]bool, maxTasks),
		baseline:      make([]int64, maxTasks),
	}, nil
}

func (m *Maintainer) AddTask(duration int64) (int, *UpdateReport, error) {
	if duration < 0 || duration > 1_000_000 {
		return -1, nil, ErrInvalidParameter
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.taskCount >= m.maxTasks {
		return -1, nil, ErrCapacityExceeded
	}
	v := m.taskCount
	oldPF := m.projectFinishLocked()
	m.duration[v] = duration
	m.snet[v] = 0
	m.fnlt[v] = -1
	startConstraint := &candidate{value: 0, edge: false, src: -1, dst: v, kind: kindStart, alive: true}
	heap.Push(&m.inHeaps[v], startConstraint)
	m.inCandidates[v] = append(m.inCandidates[v], startConstraint)
	deadlineConstraint := &candidate{value: m.deadline, edge: false, src: v, dst: -1, kind: kindDeadline, alive: true}
	heap.Push(&m.outHeaps[v], deadlineConstraint)
	m.outCandidates[v] = append(m.outCandidates[v], deadlineConstraint)
	m.taskCount++
	return v, m.updateLocked([]int{v}, []int{v}, nil, nil, map[int]struct{}{v: {}}, false, oldPF), nil
}

func (m *Maintainer) AddDep(u, v int, lag int64) (*UpdateReport, error) {
	if lag < -1_000_000 || lag > 1_000_000 {
		return nil, ErrInvalidParameter
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if u < 0 || u >= m.taskCount || v < 0 || v >= m.taskCount {
		return nil, ErrTaskNotFound
	}
	key := [2]int{u, v}
	if _, exists := m.edges[key]; exists {
		return nil, ErrDependencyExists
	}
	if m.depCount >= m.maxDeps {
		return nil, ErrCapacityExceeded
	}
	if u == v || m.reachesLocked(v, u) {
		return nil, ErrCycle
	}
	oldPF := m.projectFinishLocked()
	fwdCandidate := &candidate{value: m.ef[u] + lag, edge: true, alive: true, src: u, dst: v, kind: kindEdge}
	heap.Push(&m.inHeaps[v], fwdCandidate)
	m.inCandidates[v] = append(m.inCandidates[v], fwdCandidate)
	bwdCandidate := &candidate{value: m.ls[v] - lag, edge: true, alive: true, src: u, dst: v, kind: kindEdge}
	heap.Push(&m.outHeaps[u], bwdCandidate)
	m.outCandidates[u] = append(m.outCandidates[u], bwdCandidate)
	edge := &edgeState{
		fwd:         fwdCandidate,
		bwd:         bwdCandidate,
		fwdVersions: []*candidate{fwdCandidate},
		bwdVersions: []*candidate{bwdCandidate},
		lag:         lag,
		from:        u,
		to:          v,
	}
	m.edges[key] = edge
	m.outEdges[u] = append(m.outEdges[u], edge)
	m.inEdges[v] = append(m.inEdges[v], edge)
	m.depCount++
	return m.updateLocked([]int{v}, []int{u, v}, map[int]struct{}{u: {}}, map[int]struct{}{u: {}}, nil, false, oldPF), nil
}

func (m *Maintainer) SetDuration(v int, duration int64) (*UpdateReport, error) {
	if duration < 0 || duration > 1_000_000 {
		return nil, ErrInvalidParameter
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if v < 0 || v >= m.taskCount {
		return nil, ErrTaskNotFound
	}
	oldPF := m.projectFinishLocked()
	m.duration[v] = duration
	force := map[int]struct{}{v: {}}
	return m.updateLocked([]int{v}, []int{v}, nil, nil, force, true, oldPF), nil
}

func (m *Maintainer) SetConstraint(v int, startNoEarlierThan, finishNoLaterThan int64) (*UpdateReport, error) {
	if startNoEarlierThan < 0 || startNoEarlierThan > 1_000_000_000 || finishNoLaterThan < -1 || finishNoLaterThan > 1_000_000_000_000 {
		return nil, ErrInvalidParameter
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if v < 0 || v >= m.taskCount {
		return nil, ErrTaskNotFound
	}
	oldPF := m.projectFinishLocked()
	m.replaceStaticStartLocked(v, startNoEarlierThan)
	m.snet[v] = startNoEarlierThan
	m.replaceStaticFinishLocked(v, finishNoLaterThan)
	m.fnlt[v] = finishNoLaterThan
	force := map[int]struct{}{v: {}}
	return m.updateLocked([]int{v}, []int{v}, nil, nil, force, false, oldPF), nil
}

func (m *Maintainer) RemoveDep(u, v int) (*UpdateReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u < 0 || u >= m.taskCount || v < 0 || v >= m.taskCount {
		return nil, ErrTaskNotFound
	}
	key := [2]int{u, v}
	edge, exists := m.edges[key]
	if !exists {
		return nil, ErrDependencyNotFound
	}
	oldPF := m.projectFinishLocked()
	for _, item := range edge.fwdVersions {
		item.alive = false
	}
	for _, item := range edge.bwdVersions {
		item.alive = false
	}
	edge.fwd = nil
	edge.bwd = nil
	m.removeEdge(&m.inEdges[v], edge)
	m.removeEdge(&m.outEdges[u], edge)
	delete(m.edges, key)
	m.depCount--
	return m.updateLocked([]int{v}, []int{u, v}, map[int]struct{}{u: {}}, map[int]struct{}{u: {}}, nil, false, oldPF), nil
}

func (m *Maintainer) CriticalPath() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.criticalPathLocked()
}

func (m *Maintainer) SetBaseline() {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy(m.baseline[:m.taskCount], m.ef[:m.taskCount])
	m.baselineTasks = m.taskCount
	m.hasBaseline = true
}

func (m *Maintainer) Variance(v int) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if v < 0 || v >= m.taskCount {
		return 0, ErrTaskNotFound
	}
	if !m.hasBaseline || v >= m.baselineTasks {
		return 0, ErrNoBaseline
	}
	return m.ef[v] - m.baseline[v], nil
}
