package defassign

import (
	"fmt"
	"sort"
	"strings"
)

// ErrCategory is the category of an input validation error. Categories are
// declared in rejection order: a lower category is reported first when an
// input violates several rules.
type ErrCategory int

const (
	// ErrCycle: the program structure tree contains a cycle or a node with
	// more than one parent (the structure is not a tree).
	ErrCycle ErrCategory = iota
	// ErrUndeclaredVar: an Assign or Read references an undeclared variable.
	ErrUndeclaredVar
	// ErrBreakOutsideLoop: a Break is not nested inside any loop.
	ErrBreakOutsideLoop
	// ErrOrphanHandler: a handler branch is not attached to exactly one Try.
	ErrOrphanHandler
)

// InputError describes a rejected input. An input that fails validation
// produces no diagnostics at all.
type InputError struct {
	Category ErrCategory
	Details  []string
}

func (e *InputError) Error() string {
	var name string
	switch e.Category {
	case ErrCycle:
		name = "structure cycle"
	case ErrUndeclaredVar:
		name = "undeclared variable"
	case ErrBreakOutsideLoop:
		name = "break outside loop"
	case ErrOrphanHandler:
		name = "handler not attached to a protected structure"
	}
	return fmt.Sprintf("invalid program (%s): %s", name, strings.Join(e.Details, "; "))
}

// Validate checks the registered program against the input rules and returns
// the first failing category, in the fixed rejection order:
// structure cycle > undeclared variable > illegal break > orphan handler.
func Validate(reg *Registry) *InputError {
	reachable, err := checkStructure(reg)
	if err != nil {
		return err
	}
	if err := checkVars(reg, reachable); err != nil {
		return err
	}
	if err := checkBreaks(reg, reachable); err != nil {
		return err
	}
	return checkHandlers(reg, reachable)
}

// checkStructure verifies that the structure reachable from the root is a
// tree: no cycles and no node with two parents. It returns the set of
// reachable node IDs.
func checkStructure(reg *Registry) (map[int]bool, *InputError) {
	root, ok := reg.Root()
	if !ok || root < 0 || root >= reg.NumNodes() {
		return nil, &InputError{Category: ErrCycle, Details: []string{"root not set or out of range"}}
	}
	const (
		white = iota
		gray
		black
	)
	n := reg.NumNodes()
	color := make([]int, n)
	reachable := make(map[int]bool, n)
	var bad []string
	type frame struct {
		id    int
		kids  []int
		index int
	}
	stack := []frame{{id: root, kids: childIDs(reg.Node(root))}}
	color[root] = gray
	reachable[root] = true
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.index >= len(top.kids) {
			color[top.id] = black
			stack = stack[:len(stack)-1]
			continue
		}
		kid := top.kids[top.index]
		top.index++
		if kid < 0 || kid >= n {
			bad = append(bad, fmt.Sprintf("node#%d: child id %d out of range", top.id, kid))
			continue
		}
		switch color[kid] {
		case white:
			color[kid] = gray
			reachable[kid] = true
			stack = append(stack, frame{id: kid, kids: childIDs(reg.Node(kid))})
		default:
			// gray: the kid is its own ancestor (cycle); black: the kid
			// already has another parent (structure is a DAG, not a tree).
			bad = append(bad, fmt.Sprintf("node#%d reached on more than one path (cycle or shared child)", kid))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, &InputError{Category: ErrCycle, Details: bad}
	}
	return reachable, nil
}

func checkVars(reg *Registry, reachable map[int]bool) *InputError {
	var bad []string
	for id := 0; id < reg.NumNodes(); id++ {
		if !reachable[id] {
			continue
		}
		n := reg.Node(id)
		if (n.Kind == KindAssign || n.Kind == KindRead) && !reg.Declared(n.Var) {
			bad = append(bad, fmt.Sprintf("node#%d references undeclared variable %q", id, n.Var))
		}
	}
	if len(bad) > 0 {
		return &InputError{Category: ErrUndeclaredVar, Details: bad}
	}
	return nil
}

func checkBreaks(reg *Registry, reachable map[int]bool) *InputError {
	var bad []string
	root, _ := reg.Root()
	type frame struct {
		id        int
		loopDepth int
	}
	stack := []frame{{id: root}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := reg.Node(f.id)
		depth := f.loopDepth
		if n.Kind == KindLoop {
			depth++
		}
		if n.Kind == KindBreak && f.loopDepth == 0 {
			bad = append(bad, fmt.Sprintf("node#%d: break not inside any loop", f.id))
		}
		for _, kid := range childIDs(n) {
			if reachable[kid] {
				stack = append(stack, frame{id: kid, loopDepth: depth})
			}
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return &InputError{Category: ErrBreakOutsideLoop, Details: bad}
	}
	return nil
}

func checkHandlers(reg *Registry, reachable map[int]bool) *InputError {
	attached := make(map[int]bool)
	var bad []string
	for id := 0; id < reg.NumNodes(); id++ {
		if !reachable[id] {
			continue
		}
		n := reg.Node(id)
		if n.Kind != KindTry {
			continue
		}
		for _, h := range n.Handlers {
			if h < 0 || h >= reg.NumNodes() || reg.Node(h).Kind != KindHandler {
				bad = append(bad, fmt.Sprintf("try#%d references non-handler node#%d", id, h))
				continue
			}
			attached[h] = true
		}
	}
	for id := 0; id < reg.NumNodes(); id++ {
		if reg.Node(id).Kind == KindHandler && !attached[id] {
			bad = append(bad, fmt.Sprintf("handler#%d does not belong to any protected structure", id))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return &InputError{Category: ErrOrphanHandler, Details: bad}
	}
	return nil
}
