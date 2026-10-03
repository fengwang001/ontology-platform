package ontology

import (
	"errors"
	"sync"
)

var (
	// ErrSize reports that an old or new file length exceeds the configured bound.
	ErrSize = errors.New("ontology: size outside allowed range")
	// ErrBadDelta reports an invalid instruction or too many instructions.
	ErrBadDelta = errors.New("ontology: invalid delta instruction")
)

// Instr is one entry in the new-file-ordered delta instruction stream.
type Instr struct {
	Kind InstrKind
	Src  int64
	Len  int64
	Data []byte
}

// InstrKind identifies a delta instruction.
type InstrKind uint8

const (
	CopyInstr InstrKind = iota + 1
	AddInstr
)

// OpKind identifies an executable in-place reconstruction operation.
type OpKind uint8

const (
	StashOp OpKind = iota + 1
	CopyOp
	UnstashOp
	AddOp
)

// Operation is one Stash, Copy, Unstash, or Add operation.
type Operation struct {
	Kind OpKind
	Src  int64
	Dst  int64
	Len  int64
	Slot int
	Data []byte
}

// PlanResult contains the executable operation sequence and plan statistics.
type PlanResult struct {
	Operations       []Operation
	StashBytes       int64
	StashedCopies    int
	Edges            int
	intervalCompares int64
}

// StatsSnapshot contains cumulative counters for accepted plans.
type StatsSnapshot struct {
	AcceptedPlans    int64
	TotalStashBytes  int64
	TotalGraphCopies int64
}

// Planner serializes accepted plans and their cumulative statistics.
type Planner struct {
	maxSize  int64
	maxInstr int

	mu             sync.Mutex
	acceptedPlans  int64
	totalStash     int64
	totalGraphCopy int64
}

type addOp struct {
	dst  int64
	data []byte
}

// NewPlanner creates a sorter with the stated old/new file and instruction bounds.
func NewPlanner(maxSize int64, maxInstr int) *Planner {
	return &Planner{
		maxSize:  maxSize,
		maxInstr: maxInstr,
	}
}

// Plan validates a delta stream and converts its non-trivial Copy instructions
// into a deterministic in-place operation schedule.
func (p *Planner) Plan(n int64, delta []Instr) (PlanResult, error) {
	if n < 0 || n > p.maxSize {
		return PlanResult{}, ErrSize
	}

	nodes := make([]copyNode, 0, len(delta))
	adds := make([]addOp, 0)
	var m int64
	for _, instr := range delta {
		dst := m
		switch instr.Kind {
		case CopyInstr:
			if instr.Len < 1 || instr.Src < 0 || instr.Src > n || instr.Len > n-instr.Src {
				return PlanResult{}, ErrBadDelta
			}
			if instr.Src != dst {
				nodes = append(nodes, copyNode{
					src: instr.Src,
					dst: dst,
					len: instr.Len,
				})
			}
		case AddInstr:
			if len(instr.Data) == 0 {
				return PlanResult{}, ErrBadDelta
			}
			data := append([]byte(nil), instr.Data...)
			adds = append(adds, addOp{dst: dst, data: data})
			m += int64(len(data))
		default:
			return PlanResult{}, ErrBadDelta
		}
		if instr.Kind == CopyInstr {
			m += instr.Len
		}
	}
	if len(delta) > p.maxInstr {
		return PlanResult{}, ErrBadDelta
	}
	if m > p.maxSize {
		return PlanResult{}, ErrSize
	}

	schedule := scheduleCopies(nodes)
	operations := make([]Operation, 0, len(schedule.copies)+2*len(schedule.stashes)+len(adds))
	for _, stash := range schedule.stashes {
		node := nodes[stash.node]
		operations = append(operations, Operation{
			Kind: StashOp,
			Src:  node.src,
			Dst:  node.dst,
			Len:  node.len,
			Slot: stash.slot,
		})
	}
	for _, nodeIndex := range schedule.copies {
		node := nodes[nodeIndex]
		operations = append(operations, Operation{
			Kind: CopyOp,
			Src:  node.src,
			Dst:  node.dst,
			Len:  node.len,
		})
	}
	for _, stash := range schedule.stashes {
		node := nodes[stash.node]
		operations = append(operations, Operation{
			Kind: UnstashOp,
			Dst:  node.dst,
			Len:  node.len,
			Slot: stash.slot,
		})
	}
	for _, add := range adds {
		operations = append(operations, Operation{
			Kind: AddOp,
			Dst:  add.dst,
			Data: add.data,
		})
	}

	result := PlanResult{
		Operations:       operations,
		StashBytes:       schedule.stashBytes,
		StashedCopies:    len(schedule.stashes),
		Edges:            schedule.edges,
		intervalCompares: schedule.intervalCompares,
	}

	p.mu.Lock()
	p.acceptedPlans++
	p.totalStash += schedule.stashBytes
	p.totalGraphCopy += int64(len(nodes))
	p.mu.Unlock()

	return result, nil
}

// Stats returns cumulative counters for all previously accepted plans.
func (p *Planner) Stats() StatsSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return StatsSnapshot{
		AcceptedPlans:    p.acceptedPlans,
		TotalStashBytes:  p.totalStash,
		TotalGraphCopies: p.totalGraphCopy,
	}
}
