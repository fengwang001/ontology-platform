package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidCapacity    = errors.New("ontology: capacity must be positive")
	ErrPartitionFull      = errors.New("ontology: partition is full")
	ErrKeyOutOfOrder      = errors.New("ontology: key is smaller than the last key")
	ErrInvalidFrameSpec   = errors.New("ontology: invalid frame specification")
	ErrInvalidFrameOrder  = errors.New("ontology: frame start must not follow frame end")
	ErrInvalidOffset      = errors.New("ontology: frame offset must not be negative")
	ErrRowIndexOutOfRange = errors.New("ontology: row index is out of range")
)

type Mode int

const (
	Rows Mode = iota
	Range
	Groups
)

type BoundType int

const (
	UnboundedPreceding BoundType = iota
	Preceding
	CurrentRowBound
	Following
	UnboundedFollowing
)

type Exclusion int

const (
	ExcludeNone Exclusion = iota
	ExcludeCurrentRow
	ExcludeGroup
	ExcludeTies
)

type Bound struct {
	Type BoundType
	N    int64
}

type FrameSpec struct {
	Mode    Mode
	Start   Bound
	End     Bound
	Exclude Exclusion
}

type Interval struct {
	Start int
	End   int
}

type FrameCalculator struct {
	mu       sync.RWMutex
	capacity int
	keys     []int64
	groups   []int64
}

func NewFrameCalculator(capacity int) (*FrameCalculator, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &FrameCalculator{
		capacity: capacity,
		keys:     make([]int64, 0, capacity),
		groups:   make([]int64, 0, capacity),
	}, nil
}

func (c *FrameCalculator) Append(key int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.keys) >= c.capacity {
		return ErrPartitionFull
	}
	if len(c.keys) > 0 && key < c.keys[len(c.keys)-1] {
		return ErrKeyOutOfOrder
	}

	group := int64(0)
	if len(c.groups) > 0 {
		group = c.groups[len(c.groups)-1]
		if key != c.keys[len(c.keys)-1] {
			group++
		}
	}
	c.keys = append(c.keys, key)
	c.groups = append(c.groups, group)
	return nil
}

func (c *FrameCalculator) Frame(i int, spec FrameSpec) ([]Interval, error) {
	if err := validateFrameSpec(spec); err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if i < 0 || i >= len(c.keys) {
		return nil, ErrRowIndexOutOfRange
	}

	keys := append([]int64(nil), c.keys...)
	groups := append([]int64(nil), c.groups...)
	return computeFrame(keys, groups, i, spec), nil
}

func validateFrameSpec(spec FrameSpec) error {
	if spec.Mode < Rows || spec.Mode > Groups {
		return ErrInvalidFrameSpec
	}
	if !validBoundType(spec.Start.Type) || !validBoundType(spec.End.Type) {
		return ErrInvalidFrameSpec
	}
	if spec.Exclude < ExcludeNone || spec.Exclude > ExcludeTies {
		return ErrInvalidFrameSpec
	}

	if spec.Start.Type == UnboundedFollowing ||
		spec.End.Type == UnboundedPreceding ||
		spec.Start.Type > spec.End.Type {
		return ErrInvalidFrameOrder
	}

	if hasOffset(spec.Start.Type) && spec.Start.N < 0 {
		return ErrInvalidOffset
	}
	if hasOffset(spec.End.Type) && spec.End.N < 0 {
		return ErrInvalidOffset
	}
	return nil
}

func validBoundType(boundType BoundType) bool {
	return boundType >= UnboundedPreceding && boundType <= UnboundedFollowing
}

func hasOffset(boundType BoundType) bool {
	return boundType == Preceding || boundType == Following
}
