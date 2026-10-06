// Package defassign implements a definite-assignment checker for a
// structured intermediate program representation.
package defassign

import (
	"fmt"
	"strings"
)

// Cond classifies the condition of an If node.
type Cond int

const (
	// CondUnknown is a condition whose value is not statically known.
	CondUnknown Cond = iota
	// CondTrue marks a condition explicitly annotated as constantly true.
	CondTrue
	// CondFalse marks a condition explicitly annotated as constantly false.
	CondFalse
)

// Kind is the kind of a program structure node.
type Kind int

const (
	KindBlock Kind = iota
	KindAssign
	KindRead
	KindIf
	KindLoop
	KindBreak
	KindReturn
	KindTry
	KindHandler
)

// Node is one node of the program structure tree. Node IDs are assigned by
// the Registry in registration order and double as source positions: a
// smaller ID means the statement appears earlier in the program.
type Node struct {
	ID          int
	Kind        Kind
	Var         string // KindAssign / KindRead
	Cond        Cond   // KindIf
	AtLeastOnce bool   // KindLoop
	Children    []int  // KindBlock
	Then        []int  // KindIf
	Else        []int  // KindIf
	Body        []int  // KindLoop / KindTry / KindHandler
	Handlers    []int  // KindTry: IDs of KindHandler nodes
	Cleanup     []int  // KindTry
}

// Registry is the program-structure registration module. Clients declare
// variables, register nodes, and finally designate a root. A Registry is
// only mutated while the program is being built; Check treats it as
// read-only, so one Registry may be checked concurrently.
type Registry struct {
	vars    map[string]bool
	nodes   []Node
	root    int
	hasRoot bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{vars: make(map[string]bool), root: -1}
}

// Declare registers a variable name.
func (r *Registry) Declare(name string) { r.vars[name] = true }

// Declared reports whether name is a declared variable.
func (r *Registry) Declared(name string) bool { return r.vars[name] }

func (r *Registry) add(n Node) int {
	n.ID = len(r.nodes)
	r.nodes = append(r.nodes, n)
	return n.ID
}

// Block registers a sequential composition of statements.
func (r *Registry) Block(stmts ...int) int {
	return r.add(Node{Kind: KindBlock, Children: stmts})
}

// Assign registers an assignment to v.
func (r *Registry) Assign(v string) int {
	return r.add(Node{Kind: KindAssign, Var: v})
}

// Read registers a read of v.
func (r *Registry) Read(v string) int {
	return r.add(Node{Kind: KindRead, Var: v})
}

// If registers a conditional. Constant conditions prune the dead side.
func (r *Registry) If(cond Cond, then, els []int) int {
	return r.add(Node{Kind: KindIf, Cond: cond, Then: then, Else: els})
}

// Loop registers a loop. Unless atLeastOnce is set, the body may execute
// zero times.
func (r *Registry) Loop(body []int, atLeastOnce bool) int {
	return r.add(Node{Kind: KindLoop, Body: body, AtLeastOnce: atLeastOnce})
}

// Break registers an early exit from the innermost enclosing loop.
func (r *Registry) Break() int { return r.add(Node{Kind: KindBreak}) }

// Return registers an early return from the program.
func (r *Registry) Return() int { return r.add(Node{Kind: KindReturn}) }

// Handler registers an exception handler branch. Every handler must be
// attached to exactly one Try node.
func (r *Registry) Handler(body ...int) int {
	return r.add(Node{Kind: KindHandler, Body: body})
}

// Try registers an exception-protected structure: a protected body, handler
// branch node IDs, and a cleanup region.
func (r *Registry) Try(body, handlers, cleanup []int) int {
	return r.add(Node{Kind: KindTry, Body: body, Handlers: handlers, Cleanup: cleanup})
}

// SetRoot designates the root of the program structure tree.
func (r *Registry) SetRoot(id int) { r.root, r.hasRoot = id, true }

// Node returns the node with the given ID.
func (r *Registry) Node(id int) *Node { return &r.nodes[id] }

// NumNodes returns the number of registered nodes.
func (r *Registry) NumNodes() int { return len(r.nodes) }

// Root returns the root node ID and whether it was set.
func (r *Registry) Root() (int, bool) { return r.root, r.hasRoot }

// childIDs lists the structural children of n in a fixed order.
func childIDs(n *Node) []int {
	switch n.Kind {
	case KindBlock:
		return n.Children
	case KindIf:
		out := make([]int, 0, len(n.Then)+len(n.Else))
		out = append(out, n.Then...)
		out = append(out, n.Else...)
		return out
	case KindLoop, KindHandler:
		return n.Body
	case KindTry:
		out := make([]int, 0, len(n.Body)+len(n.Handlers)+len(n.Cleanup))
		out = append(out, n.Body...)
		out = append(out, n.Handlers...)
		out = append(out, n.Cleanup...)
		return out
	default:
		return nil
	}
}

// Path-token constructors. They are the single source of the path vocabulary
// shared by the flow analysis and by the independent naive model used in
// tests, so both render identical path descriptions.
func tokIfThen(id int) string   { return fmt.Sprintf("if#%d:then", id) }
func tokIfElse(id int) string   { return fmt.Sprintf("if#%d:else", id) }
func tokLoopZero(id int) string { return fmt.Sprintf("loop#%d:zero", id) }
func tokLoopBody(id int) string { return fmt.Sprintf("loop#%d:body", id) }
func tokBreak(id int) string    { return fmt.Sprintf("break#%d", id) }
func tokReturn(id int) string   { return fmt.Sprintf("return#%d", id) }
func tokTryBody(id int) string  { return fmt.Sprintf("try#%d:body", id) }
func tokTryHandler(id, i int) string {
	return fmt.Sprintf("try#%d:handler-%d", id, i)
}
func tokTryUnhandled(id int) string { return fmt.Sprintf("try#%d:unhandled", id) }

// renderPath renders a decision-token list as a human-readable path.
func renderPath(path []string) string {
	if len(path) == 0 {
		return "<entry>"
	}
	return strings.Join(path, " -> ")
}

// String renders the registered program tree for logs and diagnostics.
func (r *Registry) String() string {
	var sb strings.Builder
	if r.hasRoot && r.root >= 0 && r.root < len(r.nodes) {
		r.writeNode(&sb, r.root, 0)
	}
	return sb.String()
}

func (r *Registry) writeNode(sb *strings.Builder, id, depth int) {
	n := &r.nodes[id]
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(sb, "%s#%d ", indent, id)
	switch n.Kind {
	case KindBlock:
		sb.WriteString("block\n")
		for _, c := range n.Children {
			r.writeNode(sb, c, depth+1)
		}
	case KindAssign:
		fmt.Fprintf(sb, "assign(%s)\n", n.Var)
	case KindRead:
		fmt.Fprintf(sb, "read(%s)\n", n.Var)
	case KindIf:
		cond := "?"
		if n.Cond == CondTrue {
			cond = "true"
		} else if n.Cond == CondFalse {
			cond = "false"
		}
		fmt.Fprintf(sb, "if[%s]\n", cond)
		r.writeList(sb, "then", n.Then, depth+1)
		r.writeList(sb, "else", n.Else, depth+1)
	case KindLoop:
		fmt.Fprintf(sb, "loop[atLeastOnce=%v]\n", n.AtLeastOnce)
		r.writeList(sb, "body", n.Body, depth+1)
	case KindBreak:
		sb.WriteString("break\n")
	case KindReturn:
		sb.WriteString("return\n")
	case KindTry:
		sb.WriteString("try\n")
		r.writeList(sb, "body", n.Body, depth+1)
		r.writeList(sb, "handlers", n.Handlers, depth+1)
		r.writeList(sb, "cleanup", n.Cleanup, depth+1)
	case KindHandler:
		sb.WriteString("handler\n")
		r.writeList(sb, "body", n.Body, depth+1)
	}
}

func (r *Registry) writeList(sb *strings.Builder, label string, ids []int, depth int) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(sb, "%s%s:\n", indent, label)
	for _, c := range ids {
		r.writeNode(sb, c, depth+1)
	}
}
