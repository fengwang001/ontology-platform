package hm

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"

	"testing"
	"time"
)

// formatType renders a type canonically. A depth cap keeps test rendering
// bounded even if a binding cycle is present (the invariant checker below
// is responsible for failing the test with a precise message).
func formatType(t Type) string {
	return formatTypeDepth(t, 0)
}

const formatMaxDepth = 10000

func formatTypeDepth(t Type, depth int) string {
	if depth > formatMaxDepth {
		return "<TOO-DEEP>"
	}
	switch x := t.(type) {
	case nil:
		return "<nil>"
	case Var:
		return fmt.Sprintf("Var%d", x.ID)
	case Con:
		parts := make([]string, len(x.Args))
		for i, a := range x.Args {
			parts[i] = formatTypeDepth(a, depth+1)
		}
		if len(parts) == 0 {
			return x.Name
		}
		return x.Name + "(" + strings.Join(parts, ",") + ")"
	case qvar:
		return fmt.Sprintf("G%d", x.idx)
	case nqvar:
		return fmt.Sprintf("G%d", x.idx)
	default:
		return "<unknown>"
	}
}

func formatPattern(p Pattern) string { return formatType(p.Body) }

// dumpSession renders the whole observable state for rejected-op checks.
func dumpSession(s *Session) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "L=%d next=%d\n", s.l, s.next)
	for i := 0; i < s.next; i++ {
		bnd := "-"
		if s.bind[i] != nil {
			bnd = formatType(s.bind[i])
		}
		fmt.Fprintf(&b, " v%d level=%d bind=%s\n", i+1, s.level[i], bnd)
	}
	for _, name := range s.envOrder {
		p := s.env[name]
		fmt.Fprintf(&b, " env %s quant=%v body=%s\n", name, p.Quant, formatType(p.Body))
	}
	return b.String()
}

// statesEquivalent compares pruned structure, survivor levels and patterns.
func statesEquivalent(t *testing.T, s *Session, n *naive) {
	t.Helper()
	s.mu.Lock()
	l, next := s.l, s.next
	canonS := canonicalSession(s)
	s.mu.Unlock()
	canonN := canonicalNaive(n)
	if l != n.l || next != n.next {
		t.Fatalf("L/next: session=%d/%d naive=%d/%d", l, next, n.l, n.next)
	}
	if canonS != canonN {
		t.Fatalf("state mismatch\n--- session ---\n%s--- naive ---\n%s", canonS, canonN)
	}
}

// assertAcyclic walks the binding graph and fails fast on any directed
// cycle, so malformed unification is reported immediately with context.
func assertAcyclic(t *testing.T, s *Session, where string) {
	t.Helper()
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, s.next+1)
	var dfs func(id int) bool
	dfs = func(id int) bool {
		color[id] = gray
		b := s.bind[id-1]
		cycle := false
		var walkTerm func(Type)
		walkTerm = func(ty Type) {
			switch z := ty.(type) {
			case Var:
				if color[z.ID] == gray {
					cycle = true
					return
				}
				if color[z.ID] == white {
					if !dfs(z.ID) {
						cycle = true
					}
				}
			case Con:
				for _, a := range z.Args {
					walkTerm(a)
					if cycle {
						return
					}
				}
			}
		}
		if b != nil {
			walkTerm(b)
		}
		color[id] = black
		return !cycle
	}
	for id := 1; id <= s.next; id++ {
		if color[id] == white && !dfs(id) {
			t.Fatalf("binding cycle after %s around Var%d; dumps:\n%s", where, id, dumpSession(s))
		}
	}
}

func canonicalSession(s *Session) string {
	var b strings.Builder
	for i := 1; i <= s.next; i++ {
		p := s.prune(Var{i})
		if v, ok := p.(Var); ok {
			fmt.Fprintf(&b, "v%d unbound level=%d\n", i, s.level[v.ID-1])
		} else {
			fmt.Fprintf(&b, "v%d -> %s\n", i, formatType(p))
		}
	}
	for _, name := range s.envOrder {
		p := s.env[name]
		fmt.Fprintf(&b, "env %s q=%v b=%s\n", name, p.Quant, formatType(p.Body))
	}
	return b.String()
}

func canonicalNaive(n *naive) string {
	var b strings.Builder
	for i := 1; i <= n.next; i++ {
		p := n.prune(Var{i})
		if v, ok := p.(Var); ok {
			fmt.Fprintf(&b, "v%d unbound level=%d\n", i, n.level[v.ID])
		} else {
			fmt.Fprintf(&b, "v%d -> %s\n", i, formatType(p))
		}
	}
	for _, name := range n.order {
		p := n.env[name]
		fmt.Fprintf(&b, "env %s q=%v b=%s\n", name, p.quant, formatType(p.body))
	}
	return b.String()
}

// ---- random operation model -------------------------------------------

type op struct {
	kind      string
	t1, t2    Type
	name      string
	expensive bool
}

type generator struct {
	rng    *rand.Rand
	ctors  []string
	arity  map[string]int
	nextID int
	budget int
}

var bindNames = []string{"a", "b", "c", "d", "e", "f"}

func (g *generator) randType(depth int) Type {
	if depth == 0 || g.rng.Intn(3) == 0 {
		if g.nextID == 0 {
			return Con{Name: "Int"}
		}
		if g.rng.Intn(8) == 0 {
			// Occasionally reference an unallocated variable id (invalid).
			return Var{ID: g.nextID + 1 + g.rng.Intn(2)}
		}
		return Var{ID: 1 + g.rng.Intn(g.nextID)}
	}
	if g.budget <= 0 {
		if g.nextID == 0 {
			return Con{Name: "Int"}
		}
		return Var{ID: 1 + g.rng.Intn(g.nextID)}
	}
	return g.randCon(depth)
}

func (g *generator) randCon(depth int) Type {
	name := g.ctors[g.rng.Intn(len(g.ctors))]
	a := g.arity[name]
	args := make([]Type, a)
	for i := range args {
		g.budget--
		args[i] = g.randType(depth - 1)
	}
	if g.rng.Intn(10) == 0 {
		bad := []string{"Nope", "List", "Int", "Fn"}[g.rng.Intn(4)]
		return Con{Name: bad, Args: args}
	}
	return Con{Name: name, Args: args}
}

func (g *generator) randName() string {
	if g.rng.Intn(10) == 0 {
		return ""
	}
	return bindNames[g.rng.Intn(len(bindNames))]
}

func (g *generator) nextOp() op {
	if g.nextID < 3 && g.rng.Intn(2) == 0 {
		g.nextID++
		return op{kind: "newvar"}
	}
	g.budget = 12
	switch g.rng.Intn(9) {
	case 0:
		return op{kind: "enter"}
	case 1:
		return op{kind: "leave"}
	case 2:
		g.nextID++
		return op{kind: "newvar"}
	case 3, 4, 5:
		return op{kind: "unify", t1: g.randType(3), t2: g.randType(3)}
	case 6:
		return op{kind: "bind", name: g.randName(), t1: g.randType(3), expensive: g.rng.Intn(2) == 0}
	case 7:
		return op{kind: "lookup", name: g.randName()}
	case 8:
		if g.rng.Intn(5) == 0 {
			id := 1
			if g.nextID > 0 {
				id = 1 + g.rng.Intn(g.nextID)
			}
			return op{kind: "level", t1: Var{ID: id}}
		}
		return op{kind: "resolve", t1: g.randType(3)}
	default:
		return op{kind: "newvar"}
	}
}

type outcome struct {
	err  string
	text string
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func outType(t Type, err error) outcome {
	if err != nil {
		return outcome{err: errText(err)}
	}
	return outcome{text: formatType(t)}
}

func runSessionOp(s *Session, o op) outcome {
	switch o.kind {
	case "newvar":
		id, err := s.NewVar()
		if err != nil {
			return outcome{err: errText(err)}
		}
		return outcome{text: fmt.Sprintf("Var%d", id)}
	case "enter":
		return outcome{err: errText(s.Enter())}
	case "leave":
		return outcome{err: errText(s.Leave())}
	case "unify":
		return outcome{err: errText(s.Unify(o.t1, o.t2))}
	case "bind":
		return outcome{err: errText(s.Bind(o.name, o.t1, o.expensive))}
	case "lookup":
		return outType(s.Lookup(o.name))
	case "resolve":
		return outType(s.Resolve(o.t1))
	case "level":
		l, err := s.Level(o.t1.(Var).ID)
		if err != nil {
			return outcome{err: errText(err)}
		}
		return outcome{text: fmt.Sprintf("L%d", l)}
	}
	return outcome{}
}

func runNaiveOp(n *naive, o op) outcome {
	switch o.kind {
	case "newvar":
		id, err := n.newVar()
		if err != nil {
			return outcome{err: errText(err)}
		}
		return outcome{text: fmt.Sprintf("Var%d", id)}
	case "enter":
		return outcome{err: errText(n.enter())}
	case "leave":
		return outcome{err: errText(n.leave())}
	case "unify":
		return outcome{err: errText(n.unify(o.t1, o.t2))}
	case "bind":
		return outcome{err: errText(n.bind(o.name, o.t1, o.expensive))}
	case "lookup":
		t, err := n.lookup(o.name)
		return outType(t, err)
	case "resolve":
		if !n.validate(o.t1) {
			return outcome{err: ErrInvalidArgument.Error()}
		}
		return outcome{text: formatType(n.resolve(o.t1))}
	case "level":
		l, err := n.levelOf(o.t1.(Var).ID)
		if err != nil {
			return outcome{err: errText(err)}
		}
		return outcome{text: fmt.Sprintf("L%d", l)}
	}
	return outcome{}
}

func describeOp(o op) string {
	switch o.kind {
	case "unify":
		return fmt.Sprintf("Unify(%s, %s)", formatType(o.t1), formatType(o.t2))
	case "bind":
		return fmt.Sprintf("Bind(%q, %s, expensive=%v)", o.name, formatType(o.t1), o.expensive)
	case "lookup":
		return fmt.Sprintf("Lookup(%q)", o.name)
	case "resolve":
		return fmt.Sprintf("Resolve(%s)", formatType(o.t1))
	case "level":
		return fmt.Sprintf("Level(%s)", formatType(o.t1))
	default:
		return o.kind + "()"
	}
}

func (o outcome) String() string {
	if o.err != "" {
		return "ERR " + o.err
	}
	if o.text != "" {
		return "OK " + o.text
	}
	return "OK"
}

func testSeed(t *testing.T) int64 {
	t.Helper()
	if raw := os.Getenv("HM_SEED"); raw != "" {
		if seed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return seed
		}
	}
	return time.Now().UnixNano()
}

func newGenerator(rng *rand.Rand) *generator {
	return &generator{
		rng:   rng,
		ctors: []string{"Int", "Bool", "List", "Fn", "Pair"},
		arity: map[string]int{"Int": 0, "Bool": 0, "List": 1, "Fn": 2, "Pair": 2},
	}
}
