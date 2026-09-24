package plan

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"

	"ontology/name"
)

var (
	ErrMissingSource   = errors.New("missing source name")
	ErrTargetExists    = errors.New("target already exists")
	ErrDuplicateSource = errors.New("duplicate source")
	ErrDuplicateTarget = errors.New("duplicate target")
	ErrCycle           = errors.New("rename cycle")
	ErrInvalidName     = errors.New("invalid name")
)

type Request struct {
	From string
	To   string
}

type Step = Request

type ConflictError struct {
	Kind   error
	Name   string
	First  string
	Second string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%v: %s %s %s", e.Kind, e.Name, e.First, e.Second)
}

func (e *ConflictError) Unwrap() error { return e.Kind }

type Plan struct {
	Edges   map[string]string
	Initial []string
	Names   []string
	Steps   []Step
	lookups int64
}

func (p *Plan) LookupCount() int64 { return p.lookups }

type stepHeap []Step

func (h stepHeap) Len() int           { return len(h) }
func (h stepHeap) Less(i, j int) bool { return h[i].From < h[j].From }
func (h stepHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *stepHeap) Push(x any)        { *h = append(*h, x.(Step)) }
func (h *stepHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func Analyze(ns *name.Namespace, requests []Request) (*Plan, error) {
	edges := make(map[string]string)
	rawFrom := make(map[string]string)
	targetOwners := make(map[string]string)
	nameSet := make(map[string]struct{})
	occupied := make(map[string]struct{})
	plan := &Plan{Edges: edges}

	ns.WithLock(func(items map[string]struct{}) {
		for item := range items {
			occupied[item] = struct{}{}
		}
	})
	conflict := func(kind error, item, first, second string) error {
		return &ConflictError{Kind: kind, Name: item, First: first, Second: second}
	}
	contains := func(m map[string]struct{}, key string) bool {
		plan.lookups++
		_, ok := m[key]
		return ok
	}

	for _, req := range requests {
		if !name.Valid(req.From) || !name.Valid(req.To) {
			return nil, ErrInvalidName
		}
		nameSet[req.From], nameSet[req.To] = struct{}{}, struct{}{}
		if !contains(occupied, req.From) {
			return nil, conflict(ErrMissingSource, req.From, "", "")
		}
		if to, ok := rawFrom[req.From]; ok && to != req.To {
			return nil, conflict(ErrDuplicateSource, req.From, to, req.To)
		}
		rawFrom[req.From] = req.To
	}
	for _, req := range requests {
		if req.From == req.To {
			continue
		}
		edges[req.From] = req.To
		if owner, ok := targetOwners[req.To]; ok && owner != req.From {
			return nil, conflict(ErrDuplicateTarget, req.To, owner, req.From)
		}
		targetOwners[req.To] = req.From
	}
	for _, req := range requests {
		if req.From == req.To {
			continue
		}
		if _, moving := edges[req.To]; !moving && contains(occupied, req.To) {
			return nil, conflict(ErrTargetExists, req.To, "", "")
		}
	}

	names := make([]string, 0, len(nameSet))
	for item := range nameSet {
		names = append(names, item)
	}
	sort.Strings(names)
	initialNames := make([]string, 0, len(occupied))
	for item := range occupied {
		initialNames = append(initialNames, item)
	}
	sort.Strings(initialNames)
	plan.Initial = initialNames
	plan.Names = names
	steps, err := order(edges, names)
	if err != nil {
		plan.Steps = nil
		return plan, err
	}
	plan.Steps = steps
	return plan, nil
}

func order(edges map[string]string, names []string) ([]Step, error) {
	waiting := make(map[string][]Step)
	ready := &stepHeap{}
	for from, to := range edges {
		if _, ok := edges[to]; ok {
			waiting[to] = append(waiting[to], Step{From: from, To: to})
		} else {
			heap.Push(ready, Step{From: from, To: to})
		}
	}
	var steps []Step
	for ready.Len() > 0 {
		step := heap.Pop(ready).(Step)
		steps = append(steps, step)
		for _, next := range waiting[step.From] {
			heap.Push(ready, next)
		}
	}
	if len(steps) != len(edges) {
		return nil, ErrCycle
	}
	return steps, nil
}
