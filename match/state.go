package match

import "sync"

const (
	maxNameBytes        = 32
	maxConstructors     = 8
	maxFields           = 4
	maxTypes            = 64
	maxSessions         = 1000
	maxArms             = 200
	maxOrBranches       = 8
	maxPatternDepth     = 16
	maxPatternExpansion = 4096
	maxSessionExpansion = 20000
)

type datatype struct {
	name         string
	constructors []*Constructor
	constructor  map[string]*Constructor
}

type session struct {
	typeName       string
	arms           []arm
	expansionCount int
	cacheValid     bool
	cache          CheckResult
}

type arm struct {
	rows      [][]Pattern
	guarded   bool
	redundant bool
}

type Checker struct {
	mu           sync.Mutex
	types        map[string]*datatype
	constructors map[string]*datatype
	sessions     map[int]*session
	missingCalls int
}

func NewChecker() *Checker {
	return &Checker{
		types:        make(map[string]*datatype),
		constructors: make(map[string]*datatype),
		sessions:     make(map[int]*session),
	}
}
