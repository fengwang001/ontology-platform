package broadphase

import (
	"errors"
	"sync"
)

const (
	minCoordinate = -1_000_000_000
	maxCoordinate = 1_000_000_000
	maxMargin     = 1_000_000
	maxFilter     = 65_535
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrObjectExists    = errors.New("object already exists")
	ErrObjectNotFound  = errors.New("object not found")
	ErrCapacityReached = errors.New("object capacity reached")
)

type Box struct {
	// LX,HX and LY,HY define the half-open box [LX,HX)×[LY,HY).
	LX int64
	HX int64
	LY int64
	HY int64
}

type Pair struct {
	// A is always smaller than B.
	A int64
	B int64
}

type Events struct {
	// FatEnter and FatExit describe broad-phase pairs using inflated boxes.
	FatEnter []Pair
	FatExit  []Pair
	// ContactEnter and ContactExit describe tight-box contacts.
	ContactEnter []Pair
	ContactExit  []Pair
}

type MoveResult struct {
	Events
	Refatted bool
}

type Broadphase struct {
	mu            sync.RWMutex
	margin        int64
	capacity      int
	count         int
	objects       map[int64]*object
	xTree         endpointTree
	yTree         endpointTree
	xIntervals    intervalTree
	fat           map[Pair]struct{}
	neighbors     map[int64]map[int64]struct{}
	contact       map[Pair]struct{}
	crossed       int
	checks        int
	contactChecks int
}

// New creates a broad-phase maintainer with margin M and object capacity C.
func New(margin int64, capacity int) (*Broadphase, error) {
	if margin < 0 || margin > maxMargin || capacity < 1 || capacity > 100_000 {
		return nil, ErrInvalidArgument
	}
	return &Broadphase{
		margin:     margin,
		capacity:   capacity,
		objects:    make(map[int64]*object),
		xTree:      newEndpointTree(0x243f_6a88_85a3_08d3),
		yTree:      newEndpointTree(0x1319_8a2e_0370_7344),
		xIntervals: newIntervalTree(0x6a09_e667_f3bc_c908),
		fat:        make(map[Pair]struct{}),
		neighbors:  make(map[int64]map[int64]struct{}),
		contact:    make(map[Pair]struct{}),
	}, nil
}
