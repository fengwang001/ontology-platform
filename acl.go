package ontology

import (
	"errors"
	"sync"
	"sync/atomic"
)

const (
	FlagOI uint8 = 1 << iota
	FlagCI
	FlagNP
	FlagIO
)

const (
	ResultGrant = iota
	ResultDenyHit
	ResultImplicitDeny
)

const RootID = "/"

type ACE struct {
	Allow     bool
	Principal []byte
	Mask      uint16
	Flags     uint8
}

type EffectiveACE struct {
	ACE
	Source []byte
	Index  int
}

type Decision struct {
	Allowed       bool
	Result        int
	DecisiveIndex int
	Source        []byte
	Inherited     bool
	Granted       uint16
}

type Error struct {
	Kind string
	Op   string
	Err  error
}

func (e *Error) Error() string {
	return e.Op + ": " + e.Kind
}

func (e *Error) Unwrap() error {
	return e.Err
}

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrLimitExceeded   = errors.New("limit exceeded")
)

type ACL struct {
	mu       sync.RWMutex
	depthMax int
	entryMax int
	version  uint64
	nodes    map[string]*node

	evalEntriesProcessed atomic.Uint64
	evalNodesVisited     atomic.Uint64
}

type node struct {
	id        string
	parent    string
	children  map[string]struct{}
	container bool
	depth     int
	protected bool
	aces      []EffectiveACE
}

func New(D int, K int) (*ACL, error) {
	if D < 1 || D > 64 || K < 1 || K > 64 {
		return nil, newACLError("New", ErrInvalidArgument)
	}
	root := &node{
		id:        RootID,
		container: true,
		children:  make(map[string]struct{}),
	}
	return &ACL{
		depthMax: D,
		entryMax: K,
		nodes:    map[string]*node{RootID: root},
	}, nil
}

func (a *ACL) Version() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.version
}

func (a *ACL) evalEntryCount() uint64 {
	return a.evalEntriesProcessed.Load()
}

func (a *ACL) evalNodeCount() uint64 {
	return a.evalNodesVisited.Load()
}
