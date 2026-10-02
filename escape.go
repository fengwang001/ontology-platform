package ontology

import "sync"

type StmtKind int

const (
	NewStmt StmtKind = iota + 1
	CopyStmt
	StoreStmt
	LoadStmt
	RetStmt
	GlobalStmt
	CallStmt
)

type Statement struct {
	Kind StmtKind
	D    int
	S    int
	G    string
	Args []int
}

type EscapeClass int

const (
	StackEscape EscapeClass = iota
	ParameterEscape
	ReturnEscape
	GlobalEscape
)

type AllocationSite struct {
	ID    int
	Class EscapeClass
}

type Summary struct {
	Ret    []bool
	Glob   []bool
	E      [][]bool
	Fresh  bool
	Rounds int
}

type Registry struct {
	mu           sync.RWMutex
	functions    map[string]*function
	order        []*function
	siteCount    int
	analysisRuns int64
}

func NewRegistry() *Registry {
	return &Registry{functions: make(map[string]*function)}
}
