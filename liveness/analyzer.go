package liveness

import "sync"

// Instruction is a single basic-block instruction described only by the
// variables it uses and defines. Within one instruction uses take effect
// before definitions, so "x = x + 1" uses x and then defines x.
type Instruction struct {
	Uses []string
	Defs []string
}

// BlockSpec is the incremental input for AddBlock. Successor ids may refer
// to blocks that have not been added yet.
type BlockSpec struct {
	ID           int
	Instructions []Instruction
	Successors   []int
}

// BlockResult is the liveness information of one block after sealing.
type BlockResult struct {
	ID            int
	UpwardExposed []string
	Defined       []string
	LiveIn        []string
	LiveOut       []string
	Successors    []int
}

type block struct {
	spec BlockSpec
	ue   map[string]struct{}
	def  map[string]struct{}
	in   map[string]struct{}
	out  map[string]struct{}
}

// Analyzer supports concurrent block insertion, a single sealing pass and
// immutable post-seal queries.
type Analyzer struct {
	mu     sync.RWMutex
	blocks map[int]*block
	order  []int
	entry  int
	sealed bool
}

// NewAnalyzer creates an empty analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{blocks: make(map[int]*block)}
}

// AddBlock records one basic block. The first successfully added block is
// the entry block. Error checks run in the order: negative id, duplicate id,
// already sealed. A rejected call leaves all recorded blocks untouched.
func (a *Analyzer) AddBlock(spec BlockSpec) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if spec.ID < 0 {
		return ErrNegativeBlockID
	}
	if _, ok := a.blocks[spec.ID]; ok {
		return ErrDuplicateBlockID
	}
	if a.sealed {
		return ErrAlreadySealed
	}

	b := &block{
		spec: BlockSpec{
			ID:           spec.ID,
			Instructions: copyInstructions(spec.Instructions),
			Successors:   copyInts(spec.Successors),
		},
		ue:  newSet(),
		def: newSet(),
		in:  newSet(),
		out: newSet(),
	}

	// Upward exposed uses: variables used before the block defines them.
	// Inside one instruction every use happens before every definition.
	defined := newSet()
	for _, ins := range b.spec.Instructions {
		for _, v := range ins.Uses {
			if !setContains(defined, v) {
				setAdd(b.ue, v)
			}
		}
		for _, v := range ins.Defs {
			setAdd(defined, v)
			setAdd(b.def, v)
		}
	}

	a.blocks[spec.ID] = b
	a.order = append(a.order, spec.ID)
	if len(a.order) == 1 {
		a.entry = spec.ID
	}
	return nil
}

// Seal validates all successor references and computes the least fixpoint of
// the backward liveness equations. Error checks run in the order: repeated
// seal, no blocks, missing successor. A failed seal leaves the analyzer
// unsealed and recorded data untouched, so more blocks can be added and Seal
// retried.
func (a *Analyzer) Seal() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sealLocked()
}
