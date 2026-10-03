package ontology

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrSize     = errors.New("ontology: size out of range")
	ErrBadDelta = errors.New("ontology: invalid delta")
)

type Copy struct {
	Src int64
	Len int64
}

type Add struct {
	Data []byte
}

type Instruction struct {
	Copy *Copy
	Add  *Add
}

type Op struct {
	Kind int
	Src  int64
	Dst  int64
	Len  int64
	Slot int
	Data []byte
}

const (
	OpStash = iota
	OpCopy
	OpUnstash
	OpAdd
)

type PlanResult struct {
	Ops              []Op
	StashBytes       int64
	StashedCopyCount int
	Edges            int64
}

type StatsSnapshot struct {
	AcceptedPlans    int64
	TotalStashBytes  int64
	TotalGraphCopies int64
}

type Sorter struct {
	maxSize  int64
	maxInstr int64

	mu               sync.Mutex
	acceptedPlans    int64
	totalStashBytes  int64
	totalGraphCopies int64
	comparisons      int64
}

func NewSorter(maxSize, maxInstr int64) *Sorter {
	return &Sorter{maxSize: maxSize, maxInstr: maxInstr}
}

func (s *Sorter) Plan(n int64, delta []Instruction) (PlanResult, error) {
	if n < 0 || n > s.maxSize {
		return PlanResult{}, ErrSize
	}

	var copies []graphCopy
	var adds []Op
	var m int64

	for _, instr := range delta {
		switch {
		case instr.Copy != nil:
			src := instr.Copy.Src
			length := instr.Copy.Len
			if length < 1 || src < 0 || src > n || length > n-src {
				return PlanResult{}, ErrBadDelta
			}
			if m > s.maxSize-length {
				m = s.maxSize + 1
			} else {
				dst := m
				m += length
				if src != dst {
					copies = append(copies, graphCopy{src: src, dst: dst, len: length})
				}
			}
		case instr.Add != nil:
			if len(instr.Add.Data) == 0 {
				return PlanResult{}, ErrBadDelta
			}
			length := int64(len(instr.Add.Data))
			if m > s.maxSize-length {
				m = s.maxSize + 1
			} else {
				dst := m
				m += length
				data := make([]byte, length)
				copy(data, instr.Add.Data)
				adds = append(adds, Op{Kind: OpAdd, Dst: dst, Data: data})
			}
		default:
			return PlanResult{}, ErrBadDelta
		}
	}
	if int64(len(delta)) > s.maxInstr {
		return PlanResult{}, ErrBadDelta
	}
	if m > s.maxSize {
		return PlanResult{}, ErrSize
	}

	order, stashed, graph := scheduleGraph(copies)

	sort.Slice(stashed, func(i, j int) bool {
		return stashed[i].dst < stashed[j].dst
	})
	sort.Slice(adds, func(i, j int) bool {
		return adds[i].Dst < adds[j].Dst
	})

	result := PlanResult{Ops: make([]Op, 0, len(stashed)*2+len(order)+len(adds))}
	for slot, copyOp := range stashed {
		result.Ops = append(result.Ops, Op{
			Kind: OpStash,
			Src:  copyOp.src,
			Len:  copyOp.len,
			Slot: slot,
		})
	}
	for _, index := range order {
		copyOp := copies[index]
		result.Ops = append(result.Ops, Op{
			Kind: OpCopy,
			Src:  copyOp.src,
			Dst:  copyOp.dst,
			Len:  copyOp.len,
		})
	}
	for slot, copyOp := range stashed {
		result.Ops = append(result.Ops, Op{
			Kind: OpUnstash,
			Dst:  copyOp.dst,
			Len:  copyOp.len,
			Slot: slot,
		})
	}
	result.Ops = append(result.Ops, adds...)

	for _, copyOp := range stashed {
		result.StashBytes += copyOp.len
	}
	result.StashedCopyCount = len(stashed)
	result.Edges = graph.edges

	s.mu.Lock()
	s.acceptedPlans++
	s.totalStashBytes += result.StashBytes
	s.totalGraphCopies += int64(len(copies))
	s.comparisons += graph.comparisons
	s.mu.Unlock()

	return result, nil
}

func (s *Sorter) Stats() StatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatsSnapshot{
		AcceptedPlans:    s.acceptedPlans,
		TotalStashBytes:  s.totalStashBytes,
		TotalGraphCopies: s.totalGraphCopies,
	}
}
