package graphdirt

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"
)

// Package graphdirt evaluates which build edges must be rebuilt.

var (
	ErrEmptyEdgeID        = errors.New("empty edge id")
	ErrEdgeExists         = errors.New("edge already exists")
	ErrNoOutputs          = errors.New("edge has no outputs")
	ErrEmptyPath          = errors.New("empty path")
	ErrOutputProduced     = errors.New("output is already produced")
	ErrInputOutputOverlap = errors.New("path is both input and output")
	ErrCycle              = errors.New("dependency cycle")

	ErrEdgeNotFound      = errors.New("edge not found")
	ErrOutputSetMismatch = errors.New("output set mismatch")
	ErrInvalidMtime      = errors.New("mtime must be at least 1")
	ErrMissingInput      = errors.New("input file is missing")

	ErrTargetNotInGraph = errors.New("target path is not in graph")
	ErrMissingSource    = errors.New("missing source input")
)

// Edge describes one build step and its three input dependency classes.
type Edge struct {
	ID              string
	Command         string
	Outputs         []string
	ExplicitInputs  []string
	ImplicitInputs  []string
	OrderOnlyInputs []string
	Restat          bool
}

// Completion is the command and explicit/implicit input maximum recorded after an edge finishes.
type Completion struct {
	Command string
	InMax   int
}

// MissingSourceError identifies the first required missing source in DirtySet.
type MissingSourceError struct {
	EdgeID string
	Path   string
}

func (e *MissingSourceError) Error() string        { return "missing source input" }
func (e *MissingSourceError) Is(target error) bool { return target == ErrMissingSource }

// TargetPathError identifies the first target that does not belong to the graph.
type TargetPathError struct {
	Path string
}

func (e *TargetPathError) Error() string        { return "target path is not in graph" }
func (e *TargetPathError) Is(target error) bool { return target == ErrTargetNotInGraph }

type edgeState struct {
	edge Edge
}

type Detector struct {
	mu        sync.RWMutex
	edges     map[string]*edgeState
	producer  map[string]string
	inGraph   map[string]struct{}
	mtimes    map[string]int
	logs      map[string]Completion
	evalCount atomic.Int64
}

// NewDetector creates an empty concurrent-safe dirty detector.
func NewDetector() *Detector {
	return &Detector{
		edges:    make(map[string]*edgeState),
		producer: make(map[string]string),
		inGraph:  make(map[string]struct{}),
		mtimes:   make(map[string]int),
		logs:     make(map[string]Completion),
	}
}

func copyStrings(values []string) []string {
	return append([]string(nil), values...)
}

// AddEdge validates and adds a build edge. Rejected calls leave state unchanged.
func (d *Detector) AddEdge(edge Edge) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if edge.ID == "" {
		return ErrEmptyEdgeID
	}
	if _, exists := d.edges[edge.ID]; exists {
		return ErrEdgeExists
	}
	if len(edge.Outputs) == 0 {
		return ErrNoOutputs
	}

	pathLists := [][]string{
		edge.Outputs,
		edge.ExplicitInputs,
		edge.ImplicitInputs,
		edge.OrderOnlyInputs,
	}
	for _, list := range pathLists {
		for _, path := range list {
			if path == "" {
				return ErrEmptyPath
			}
		}
	}

	for _, output := range edge.Outputs {
		if _, exists := d.producer[output]; exists {
			return ErrOutputProduced
		}
	}

	inputs := make(map[string]struct{}, len(edge.ExplicitInputs)+len(edge.ImplicitInputs)+len(edge.OrderOnlyInputs))
	for _, list := range [][]string{edge.ExplicitInputs, edge.ImplicitInputs, edge.OrderOnlyInputs} {
		for _, path := range list {
			inputs[path] = struct{}{}
		}
	}
	for _, output := range edge.Outputs {
		if _, isInput := inputs[output]; isInput {
			return ErrInputOutputOverlap
		}
	}

	if d.wouldCycle(edge) {
		return ErrCycle
	}

	copied := edge
	copied.Outputs = copyStrings(edge.Outputs)
	copied.ExplicitInputs = copyStrings(edge.ExplicitInputs)
	copied.ImplicitInputs = copyStrings(edge.ImplicitInputs)
	copied.OrderOnlyInputs = copyStrings(edge.OrderOnlyInputs)
	d.edges[edge.ID] = &edgeState{edge: copied}

	for _, output := range copied.Outputs {
		d.producer[output] = copied.ID
		d.inGraph[output] = struct{}{}
	}
	for path := range inputs {
		d.inGraph[path] = struct{}{}
	}
	return nil
}

func (d *Detector) wouldCycle(edge Edge) bool {
	newOutputs := make(map[string]struct{}, len(edge.Outputs))
	for _, output := range edge.Outputs {
		newOutputs[output] = struct{}{}
	}

	visited := map[string]bool{edge.ID: true}
	pending := make([]string, 0)

	for _, list := range [][]string{edge.ExplicitInputs, edge.ImplicitInputs, edge.OrderOnlyInputs} {
		for _, path := range list {
			producer, exists := d.producer[path]
			if _, isNewOutput := newOutputs[path]; isNewOutput {
				producer, exists = edge.ID, true
			}
			if exists {
				if producer == edge.ID || !visited[producer] {
					pending = append(pending, producer)
				}
			}
		}
	}

	for len(pending) > 0 {
		currentID := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if currentID == edge.ID {
			return true
		}
		if visited[currentID] {
			continue
		}
		visited[currentID] = true

		current := d.edges[currentID]
		for _, list := range [][]string{current.edge.ExplicitInputs, current.edge.ImplicitInputs, current.edge.OrderOnlyInputs} {
			for _, path := range list {
				producer, exists := d.producer[path]
				if _, isNewOutput := newOutputs[path]; isNewOutput {
					producer, exists = edge.ID, true
				}
				if exists && (producer == edge.ID || !visited[producer]) {
					pending = append(pending, producer)
				}
			}
		}
	}
	return false
}

// SetMtime records a positive file modification time.
func (d *Detector) SetMtime(path string, t int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if path == "" {
		return ErrEmptyPath
	}
	if t < 1 {
		return ErrInvalidMtime
	}
	d.mtimes[path] = t
	return nil
}

// Remove marks a path absent; removing an already absent path succeeds.
func (d *Detector) Remove(path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if path == "" {
		return ErrEmptyPath
	}
	delete(d.mtimes, path)
	return nil
}

// Complete records a successful build and refreshes all edge outputs.
func (d *Detector) Complete(edgeID string, outputs map[string]int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	current, exists := d.edges[edgeID]
	if !exists {
		return ErrEdgeNotFound
	}

	outputSet := make(map[string]struct{}, len(current.edge.Outputs))
	for _, output := range current.edge.Outputs {
		outputSet[output] = struct{}{}
	}
	if len(outputs) != len(outputSet) {
		return ErrOutputSetMismatch
	}
	for path := range outputs {
		if _, expected := outputSet[path]; !expected {
			return ErrOutputSetMismatch
		}
	}

	for _, t := range outputs {
		if t < 1 {
			return ErrInvalidMtime
		}
	}

	inputMax := 0
	for _, list := range [][]string{current.edge.ExplicitInputs, current.edge.ImplicitInputs} {
		for _, path := range list {
			t, exists := d.mtimes[path]
			if !exists {
				return ErrMissingInput
			}
			if t > inputMax {
				inputMax = t
			}
		}
	}

	for output, t := range outputs {
		d.mtimes[output] = t
	}
	d.logs[edgeID] = Completion{Command: current.edge.Command, InMax: inputMax}
	return nil
}

// DirtySet returns dirty edges in the target closure sorted by edge ID.
func (d *Detector) DirtySet(targets []string) ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, target := range targets {
		if _, exists := d.inGraph[target]; !exists {
			return nil, &TargetPathError{Path: target}
		}
	}

	closure := make(map[string]struct{})
	queued := make(map[string]struct{})
	pending := make([]string, 0)
	for _, target := range targets {
		if edgeID, exists := d.producer[target]; exists {
			if _, seen := queued[edgeID]; !seen {
				queued[edgeID] = struct{}{}
				pending = append(pending, edgeID)
			}
		}
	}

	for len(pending) > 0 {
		edgeID := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, seen := closure[edgeID]; seen {
			continue
		}
		closure[edgeID] = struct{}{}

		edge := d.edges[edgeID]
		for _, list := range [][]string{edge.edge.ExplicitInputs, edge.edge.ImplicitInputs, edge.edge.OrderOnlyInputs} {
			for _, path := range list {
				if producer, exists := d.producer[path]; exists {
					if _, seen := queued[producer]; !seen {
						queued[producer] = struct{}{}
						pending = append(pending, producer)
					}
				}
			}
		}
	}

	closureIDs := make([]string, 0, len(closure))
	for edgeID := range closure {
		closureIDs = append(closureIDs, edgeID)
	}
	sort.Strings(closureIDs)

	for _, edgeID := range closureIDs {
		edge := d.edges[edgeID]
		for _, list := range [][]string{edge.edge.ExplicitInputs, edge.edge.ImplicitInputs, edge.edge.OrderOnlyInputs} {
			for _, path := range list {
				if _, produced := d.producer[path]; !produced {
					if _, exists := d.mtimes[path]; !exists {
						return nil, &MissingSourceError{EdgeID: edgeID, Path: path}
					}
				}
			}
		}
	}

	order := make([]string, 0, len(closure))
	visited := make(map[string]struct{}, len(closure))
	type stackItem struct {
		edgeID   string
		expanded bool
	}
	stack := make([]stackItem, 0)
	for _, rootID := range closureIDs {
		if _, seen := visited[rootID]; seen {
			continue
		}
		stack = append(stack, stackItem{edgeID: rootID})
		for len(stack) > 0 {
			item := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if item.expanded {
				order = append(order, item.edgeID)
				continue
			}
			if _, seen := visited[item.edgeID]; seen {
				continue
			}
			visited[item.edgeID] = struct{}{}
			stack = append(stack, stackItem{edgeID: item.edgeID, expanded: true})

			current := d.edges[item.edgeID]
			for listIndex := 2; listIndex >= 0; listIndex-- {
				lists := [][]string{current.edge.ExplicitInputs, current.edge.ImplicitInputs, current.edge.OrderOnlyInputs}
				for _, path := range lists[listIndex] {
					if producer, exists := d.producer[path]; exists {
						if _, seen := visited[producer]; !seen {
							stack = append(stack, stackItem{edgeID: producer})
						}
					}
				}
			}
		}
	}

	dirty := make(map[string]bool, len(closure))
	for _, edgeID := range order {
		d.evalCount.Add(1)
		edge := d.edges[edgeID]
		isDirty := d.selfDirtyLocked(edge)

		for _, list := range [][]string{edge.edge.ExplicitInputs, edge.edge.ImplicitInputs} {
			for _, path := range list {
				if producer, exists := d.producer[path]; exists && dirty[producer] {
					isDirty = true
				}
			}
		}
		dirty[edgeID] = isDirty
	}

	result := make([]string, 0)
	for _, edgeID := range closureIDs {
		if dirty[edgeID] {
			result = append(result, edgeID)
		}
	}
	return result, nil
}

func (d *Detector) selfDirtyLocked(state *edgeState) bool {
	edge := state.edge
	log, completed := d.logs[edge.ID]
	if !completed || log.Command != edge.Command {
		return true
	}

	minOutput := 0
	for index, output := range edge.Outputs {
		t, exists := d.mtimes[output]
		if !exists {
			return true
		}
		if index == 0 || t < minOutput {
			minOutput = t
		}
	}

	inputMax := 0
	for _, list := range [][]string{edge.ExplicitInputs, edge.ImplicitInputs} {
		for _, input := range list {
			if t, exists := d.mtimes[input]; exists && t > inputMax {
				inputMax = t
			}
		}
	}

	if edge.Restat {
		return log.InMax < inputMax
	}
	return minOutput < inputMax
}

func (d *Detector) resetEvalCount() int64 {
	return d.evalCount.Swap(0)
}

func (d *Detector) evalCountValue() int64 {
	return d.evalCount.Load()
}
