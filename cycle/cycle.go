package cycle

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"

	"ontology/plan"
)

var ErrNoTemp = errors.New("no available temporary name")

type Result struct {
	Steps     []plan.Step
	TempNames []string
	Cycles    [][]string
}

type heapStep = plan.Step
type stepHeap []heapStep

func (h stepHeap) Len() int           { return len(h) }
func (h stepHeap) Less(i, j int) bool { return h[i].From < h[j].From }
func (h stepHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *stepHeap) Push(x any)        { *h = append(*h, x.(heapStep)) }
func (h *stepHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func Break(p *plan.Plan, maxTemp int) (*Result, error) {
	cycles := findCycles(p)
	steps := make(map[string]plan.Step)
	cycleEdge := make(map[string]int)
	cycleStarts := make(map[string]struct{})
	temps := make(map[string]struct{})
	used := make(map[string]struct{})
	var tempNames []string

	for _, name := range p.Initial {
		used[name] = struct{}{}
	}
	for _, name := range p.Names {
		used[name] = struct{}{}
	}
	for _, cyc := range cycles {
		start := cyc[0]
		cycleStarts[start] = struct{}{}
		temp, err := chooseTemp(used, temps, maxTemp)
		if err != nil {
			return nil, err
		}
		tempNames = append(tempNames, temp)
		steps[start] = plan.Step{From: start, To: temp}
		for i, from := range cyc {
			cycleEdge[from] = i
			if from != start {
				steps[from] = plan.Step{From: from, To: cyc[(i+1)%len(cyc)]}
			}
		}
		steps[temp] = plan.Step{From: temp, To: cyc[1]}
	}
	for from, to := range p.Edges {
		if _, isCycleEdge := cycleEdge[from]; isCycleEdge {
			continue
		}
		if _, isCycleStart := cycleStarts[to]; isCycleStart {
			continue
		}
		steps[from] = plan.Step{From: from, To: to}
	}

	initial := make(map[string]struct{})
	for _, item := range p.Initial {
		initial[item] = struct{}{}
	}
	ordered, err := schedule(initial, steps)
	if err != nil {
		return nil, err
	}
	return &Result{Steps: ordered, TempNames: tempNames, Cycles: cycles}, nil
}

func findCycles(p *plan.Plan) [][]string {
	state := make(map[string]int)
	var cycles [][]string
	var dfs func(string, []string)
	dfs = func(from string, stack []string) {
		state[from] = 1
		stack = append(stack, from)
		if to, ok := p.Edges[from]; ok {
			switch state[to] {
			case 0:
				dfs(to, stack)
			case 1:
				for i, item := range stack {
					if item == to {
						cycles = append(cycles, append([]string(nil), stack[i:]...))
						break
					}
				}
			}
		}
		state[from] = 2
	}
	for _, from := range p.Names {
		if _, ok := p.Edges[from]; ok && state[from] == 0 {
			dfs(from, nil)
		}
	}
	sort.Slice(cycles, func(i, j int) bool { return cycles[i][0] < cycles[j][0] })
	return cycles
}

func chooseTemp(used map[string]struct{}, temps map[string]struct{}, maxTemp int) (string, error) {
	for i := 0; i < maxTemp; i++ {
		candidate := fmt.Sprintf("__rename_tmp_%d__", i)
		if _, a := used[candidate]; a {
			continue
		}
		if _, b := temps[candidate]; b {
			continue
		}
		temps[candidate] = struct{}{}
		return candidate, nil
	}
	return "", ErrNoTemp
}

func schedule(initial map[string]struct{}, stepsMap map[string]plan.Step) ([]plan.Step, error) {
	occupied := make(map[string]struct{}, len(initial))
	for item := range initial {
		occupied[item] = struct{}{}
	}
	waiting := make(map[string][]plan.Step)
	ready := &stepHeap{}
	for _, step := range stepsMap {
		if _, busy := occupied[step.To]; busy {
			waiting[step.To] = append(waiting[step.To], step)
			continue
		}
		heap.Push(ready, step)
	}
	var out []plan.Step
	for ready.Len() > 0 {
		step := heap.Pop(ready).(plan.Step)
		out = append(out, step)
		for _, next := range waiting[step.From] {
			heap.Push(ready, next)
		}
	}
	if len(out) != len(stepsMap) {
		return nil, errors.New("cycle: unable to schedule expanded steps")
	}
	return out, nil
}
