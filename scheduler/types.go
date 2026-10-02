package scheduler

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrTaskNotFound    = errors.New("task not found")
	ErrDepExists       = errors.New("dependency already exists")
	ErrTaskLimit       = errors.New("task limit reached")
	ErrDepLimit        = errors.New("dependency limit reached")
	ErrDepNotFound     = errors.New("dependency not found")
	ErrCycle           = errors.New("dependency cycle")
	ErrNoBaseline      = errors.New("no baseline")
)

type UpdateReport struct {
	ChangedES   []int
	ChangedLF   []int
	OldPF       int64
	NewPF       int64
	CritAdded   []int
	CritRemoved []int
}

type Scheduler struct {
	mu sync.RWMutex

	taskLimit int
	edgeLimit int
	deadline  int64
	taskCount int
	edgeCount int

	duration []int64
	snet     []int64
	fnlt     []int64
	es       []int64
	ef       []int64
	ls       []int64
	lf       []int64
	tf       []int64

	preds [][]int
	succs [][]int
	edges map[int64]int64

	pf int64

	critical      []bool
	minTF         int64
	tfCount       map[int64]int
	criticalCount int

	baselineSet bool
	baselineEF  []int64

	fwdEval int
	bwdEval int

	order         []int
	pos           []int
	reachableMark []uint32
	reachableGen  uint32
}

func New(nLimit, eLimit, deadline int64) (*Scheduler, error) {
	if nLimit < 1 || nLimit > 100000 ||
		eLimit < 1 || eLimit > 500000 ||
		deadline < 0 || deadline > 1_000_000_000_000 {
		return nil, fmt.Errorf("%w: constructor limits are out of range", ErrInvalidArgument)
	}
	n := int(nLimit)
	s := &Scheduler{
		taskLimit:     n,
		edgeLimit:     int(eLimit),
		deadline:      deadline,
		duration:      make([]int64, 0, n),
		snet:          make([]int64, 0, n),
		fnlt:          make([]int64, 0, n),
		es:            make([]int64, 0, n),
		ef:            make([]int64, 0, n),
		ls:            make([]int64, 0, n),
		lf:            make([]int64, 0, n),
		tf:            make([]int64, 0, n),
		preds:         make([][]int, 0, n),
		succs:         make([][]int, 0, n),
		edges:         make(map[int64]int64),
		critical:      make([]bool, 0, n),
		tfCount:       make(map[int64]int),
		reachableMark: make([]uint32, n),
	}
	return s, nil
}
