// Package escape implements a function-granularity escape analysis summary
// calculator. Functions are registered one at a time; each registration runs
// a flow-insensitive, field-insensitive points-to analysis over the function
// body, classifies its allocation sites, and records a summary that later
// registrations use to analyze calls.
package escape

import (
	"errors"
	"fmt"
	"sync"
)

const (
	maxNameLen   = 32
	maxParams    = 8
	maxVars      = 32
	maxStmts     = 64
	maxArgs      = 8
	maxFunctions = 256
)

// Rejection reasons, distinguishable via errors.Is.
var (
	ErrInvalidArgument  = errors.New("escape: invalid argument")
	ErrNameExists       = errors.New("escape: function already registered")
	ErrTooManyFunctions = errors.New("escape: function count limit exceeded")
	ErrUnknownCallee    = errors.New("escape: callee is neither registered nor the function itself")
	ErrArgCountMismatch = errors.New("escape: argument count does not match callee parameters")
	ErrNotExist         = errors.New("escape: function not registered")
)

// Kind is the statement opcode.
type Kind int

const (
	New    Kind = iota // New(d): allocate at d
	Copy               // Copy(d, s): d = s
	Store              // Store(d, s): *d = s
	Load               // Load(d, s): d = *s
	Ret                // Ret(s): return s
	Global             // Global(s): s escapes globally
	Call               // Call(d, g, args): d = g(args...), d == -1 discards
)

// Stmt is one function statement. Fields are interpreted per Kind:
// New/Copy/Store/Load use D (and S except New), Ret/Global use S,
// Call uses D (-1 allowed), G and Args.
type Stmt struct {
	Kind Kind
	D    int
	S    int
	G    string
	Args []int
}

// Class is the escape classification of an allocation site.
type Class int

const (
	Stack        Class = iota // stays on the stack
	GlobalEscape              // reachable from a global-mark seed
	ReturnEscape              // reachable from the return set
	ParamEscape               // reachable from a parameter object via >=1 heap step
)

func (c Class) String() string {
	switch c {
	case Stack:
		return "stack"
	case GlobalEscape:
		return "global"
	case ReturnEscape:
		return "return"
	case ParamEscape:
		return "param"
	}
	return fmt.Sprintf("Class(%d)", int(c))
}

// Summary is the escape summary of a registered function.
type Summary struct {
	Ret   []bool   // Ret[i]: parameter i may flow into the return-reachable closure
	Glob  []bool   // Glob[i]: parameter i may be globally marked
	E     [][]bool // E[i][j]: param object Pj is a direct element of heap(Pi)
	Fresh bool     // the return set directly contains an allocation site or call-result object
}

func (s Summary) clone() Summary {
	out := Summary{
		Ret:   append([]bool(nil), s.Ret...),
		Glob:  append([]bool(nil), s.Glob...),
		Fresh: s.Fresh,
	}
	out.E = make([][]bool, len(s.E))
	for i := range s.E {
		out.E[i] = append([]bool(nil), s.E[i]...)
	}
	return out
}

func emptySummary(k int) Summary {
	e := make([][]bool, k)
	for i := range e {
		e[i] = make([]bool, k)
	}
	return Summary{Ret: make([]bool, k), Glob: make([]bool, k), E: e}
}

func summaryEqual(a, b Summary) bool {
	if a.Fresh != b.Fresh || len(a.Ret) != len(b.Ret) || len(a.Glob) != len(b.Glob) || len(a.E) != len(b.E) {
		return false
	}
	for i := range a.Ret {
		if a.Ret[i] != b.Ret[i] || a.Glob[i] != b.Glob[i] {
			return false
		}
	}
	for i := range a.E {
		if len(a.E[i]) != len(b.E[i]) {
			return false
		}
		for j := range a.E[i] {
			if a.E[i][j] != b.E[i][j] {
				return false
			}
		}
	}
	return true
}

// Site pairs an allocation site id with its escape classification.
type Site struct {
	ID    int
	Class Class
}

type function struct {
	name    string
	k       int
	v       int
	stmts   []Stmt
	summary Summary
	rounds  int
	sites   []Site
}

// Analyzer registers functions and answers summary/site queries. All methods
// are safe for concurrent use; the result is equivalent to some serial order.
type Analyzer struct {
	mu           sync.Mutex
	funcs        map[string]*function
	nextSite     int
	analyzeCount int // total analysis rounds executed (for verification)
}

// NewAnalyzer returns an empty Analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{funcs: make(map[string]*function), nextSite: 1}
}

// Register validates and registers a function, running the escape analysis.
// On any error the analyzer state (including the allocation site counter) is
// left untouched.
func (a *Analyzer) Register(name string, k, v int, stmts []Stmt) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := validateArgs(name, k, v, stmts); err != nil {
		return err
	}
	if _, ok := a.funcs[name]; ok {
		return fmt.Errorf("%w: %q", ErrNameExists, name)
	}
	if len(a.funcs) >= maxFunctions {
		return fmt.Errorf("%w: %d functions already registered", ErrTooManyFunctions, maxFunctions)
	}
	for _, s := range stmts {
		if s.Kind != Call {
			continue
		}
		ck := k
		if s.G != name {
			callee, ok := a.funcs[s.G]
			if !ok {
				return fmt.Errorf("%w: %q", ErrUnknownCallee, s.G)
			}
			ck = callee.k
		}
		if len(s.Args) != ck {
			return fmt.Errorf("%w: call to %q passes %d args, want %d", ErrArgCountMismatch, s.G, len(s.Args), ck)
		}
	}

	rec := &function{name: name, k: k, v: v, stmts: append([]Stmt(nil), stmts...)}
	var siteIDs []int
	for _, s := range stmts {
		if s.Kind == New {
			siteIDs = append(siteIDs, a.nextSite)
			a.nextSite++
		}
	}

	selfCall := false
	for _, s := range stmts {
		if s.Kind == Call && s.G == name {
			selfCall = true
			break
		}
	}

	used := emptySummary(k)
	rounds := 0
	for {
		rounds++
		a.analyzeCount++
		sums := make([]Summary, len(stmts))
		for i, s := range stmts {
			if s.Kind != Call {
				continue
			}
			if s.G == name {
				sums[i] = used
			} else {
				sums[i] = a.funcs[s.G].summary
			}
		}
		res := analyze(k, v, stmts, sums)
		if !selfCall || summaryEqual(res.summary, used) {
			rec.summary = res.summary
			rec.rounds = rounds
			for i, id := range siteIDs {
				rec.sites = append(rec.sites, Site{ID: id, Class: res.classes[i]})
			}
			break
		}
		used = res.summary
		if rounds > 2*k+k*k+2 {
			panic("escape: summary fixpoint did not converge")
		}
	}

	a.funcs[name] = rec
	return nil
}

// Summary returns the escape summary and the number of analysis rounds for a
// registered function.
func (a *Analyzer) Summary(name string) (Summary, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.funcs[name]
	if !ok {
		return Summary{}, 0, fmt.Errorf("%w: %q", ErrNotExist, name)
	}
	return rec.summary.clone(), rec.rounds, nil
}

// Sites returns the allocation sites of a registered function in ascending
// id order, with their classifications.
func (a *Analyzer) Sites(name string) ([]Site, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.funcs[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotExist, name)
	}
	return append([]Site(nil), rec.sites...), nil
}

func validateArgs(name string, k, v int, stmts []Stmt) error {
	if len(name) == 0 || len(name) > maxNameLen {
		return fmt.Errorf("%w: name length %d not in [1,%d]", ErrInvalidArgument, len(name), maxNameLen)
	}
	if k < 0 || k > maxParams {
		return fmt.Errorf("%w: k=%d not in [0,%d]", ErrInvalidArgument, k, maxParams)
	}
	if v < k || v > maxVars {
		return fmt.Errorf("%w: V=%d not in [%d,%d]", ErrInvalidArgument, v, k, maxVars)
	}
	if len(stmts) > maxStmts {
		return fmt.Errorf("%w: %d statements, max %d", ErrInvalidArgument, len(stmts), maxStmts)
	}
	checkVar := func(x int) error {
		if x < 0 || x >= v {
			return fmt.Errorf("%w: variable %d out of range [0,%d)", ErrInvalidArgument, x, v)
		}
		return nil
	}
	for _, s := range stmts {
		switch s.Kind {
		case New:
			if err := checkVar(s.D); err != nil {
				return err
			}
		case Copy, Store, Load:
			if err := checkVar(s.D); err != nil {
				return err
			}
			if err := checkVar(s.S); err != nil {
				return err
			}
		case Ret, Global:
			if err := checkVar(s.S); err != nil {
				return err
			}
		case Call:
			if s.D != -1 {
				if err := checkVar(s.D); err != nil {
					return err
				}
			}
			if len(s.Args) > maxArgs {
				return fmt.Errorf("%w: %d call args, max %d", ErrInvalidArgument, len(s.Args), maxArgs)
			}
			for _, x := range s.Args {
				if err := checkVar(x); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("%w: unknown statement kind %d", ErrInvalidArgument, int(s.Kind))
		}
		if s.Kind != Call && s.Kind != Ret && s.Kind != Global && s.D == -1 {
			return fmt.Errorf("%w: d=-1 only allowed for Call", ErrInvalidArgument)
		}
	}
	return nil
}
