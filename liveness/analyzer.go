package liveness

import (
	"fmt"
	"sort"
	"sync"
)

// Instruction 描述一条指令：Uses 为本指令使用的变量，
// Defs 为本指令定义的变量。同一条指令内先使用后定义，
// 因此 x = x + 1 中 x 同时出现在 Uses 与 Defs，且算作使用。
type Instruction struct {
	Uses []string
	Defs []string
}

// BlockSnapshot 是封口后某块分析结果的稳定快照。
// 所有集合均已按字典序排序，可直接用于输出或重放比对。
type BlockSnapshot struct {
	ID      int
	UE      []string
	Def     []string
	LiveIn  []string
	LiveOut []string
}

// block 保存一块的录入数据与（封口后的）求解结果。
type block struct {
	id      int
	insts   []Instruction
	succ    []int
	ue      map[string]struct{}
	def     map[string]struct{}
	liveIn  map[string]struct{}
	liveOut map[string]struct{}
}

// Analyzer 支持并发录入基本块、一次性封口求解、封口后并发查询。
// 所有方法在同一把互斥锁下串行化，因此并发调用的结果等价于
// 某个串行顺序；封口成功后结果不可变。
type Analyzer struct {
	mu     sync.Mutex
	blocks map[int]*block
	order  []int // 成功录入的块编号，按录入先后；首个即入口
	sealed bool
}

// NewAnalyzer 创建一个空的分析器。
func NewAnalyzer() *Analyzer {
	return &Analyzer{blocks: make(map[int]*block)}
}

// AddBlock 录入一个基本块。
// 报错优先级：块编号为负 → 块编号已存在 → 封口后添加。
// 被拒绝时不改变任何已录入状态。
func (a *Analyzer) AddBlock(id int, instructions []Instruction, successors []int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id < 0 {
		return &AnalysisError{Code: ErrNegativeBlockID, Detail: fmt.Sprintf("block id %d is negative", id)}
	}
	if _, ok := a.blocks[id]; ok {
		return &AnalysisError{Code: ErrDuplicateBlockID, Detail: fmt.Sprintf("block id %d already exists", id)}
	}
	if a.sealed {
		return &AnalysisError{Code: ErrAlreadySealed, Detail: "analyzer is sealed"}
	}

	b := &block{
		id:    id,
		insts: copyInstructions(instructions),
		succ:  copyInts(successors),
		ue:    make(map[string]struct{}),
		def:   make(map[string]struct{}),
	}
	computeUEAndDef(b)
	a.blocks[id] = b
	a.order = append(a.order, id)
	return nil
}

// Seal 校验后继引用并计算后向数据流的最小不动点。
// 报错优先级：重复封口 → 没有任何块 → 后继块不存在。
// 后继不存在时，按“引用方块编号升序、同一块内后继出现序”
// 报告第一处。封口失败不改变已录入的块，可继续添加后重试。
func (a *Analyzer) Seal() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sealed {
		return &AnalysisError{Code: ErrSealedTwice, Detail: "analyzer is already sealed"}
	}
	if len(a.blocks) == 0 {
		return &AnalysisError{Code: ErrNoBlocks, Detail: "no blocks have been added"}
	}

	refs := sortedInts(a.blocks)
	for _, id := range refs {
		b := a.blocks[id]
		for _, s := range b.succ {
			if _, ok := a.blocks[s]; !ok {
				return &AnalysisError{
					Code:   ErrMissingSuccessor,
					Detail: fmt.Sprintf("block %d references missing successor %d", id, s),
				}
			}
		}
	}

	solve(a.blocks, refs)

	a.sealed = true
	return nil
}

// solve 以朴素逐轮迭代求最小不动点：
//
//	LiveOut(B) = ∪_{S ∈ succ(B)} LiveIn(S)      （无后继则为空集）
//	LiveIn(B)  = UE(B) ∪ (LiveOut(B) − Def(B))
//
// 各集合初值为空；每轮按块编号升序更新，直到一轮内无变化。
// 单调算子从空集出发的极限即最小不动点，且迭代顺序固定，
// 相同输入重放得到逐位相同的结果。
func solve(blocks map[int]*block, refs []int) {
	liveIn := make(map[int]map[string]struct{}, len(refs))
	liveOut := make(map[int]map[string]struct{}, len(refs))
	for _, id := range refs {
		liveIn[id] = make(map[string]struct{})
		liveOut[id] = make(map[string]struct{})
	}

	for {
		changed := false
		for _, id := range refs {
			b := blocks[id]

			newOut := make(map[string]struct{})
			for _, s := range b.succ {
				for v := range liveIn[s] {
					newOut[v] = struct{}{}
				}
			}

			newIn := make(map[string]struct{})
			for v := range b.ue {
				newIn[v] = struct{}{}
			}
			for v := range newOut {
				if _, defined := b.def[v]; !defined {
					newIn[v] = struct{}{}
				}
			}

			if !setEqual(newOut, liveOut[id]) {
				liveOut[id] = newOut
				changed = true
			}
			if !setEqual(newIn, liveIn[id]) {
				liveIn[id] = newIn
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	for _, id := range refs {
		blocks[id].liveIn = liveIn[id]
		blocks[id].liveOut = liveOut[id]
	}
}

// computeUEAndDef 顺序扫描指令：变量在首次被本块定义之前使用，
// 即进入 UE（上行暴露使用）；Def 收集块内全部定义。
// 同一条指令内先使用后定义，因此该指令 Defs 中的变量
// 仍可由同指令 Uses 进入 UE。
func computeUEAndDef(b *block) {
	defined := make(map[string]struct{})
	for _, inst := range b.insts {
		for _, v := range inst.Uses {
			if _, ok := defined[v]; !ok {
				b.ue[v] = struct{}{}
			}
		}
		for _, v := range inst.Defs {
			b.def[v] = struct{}{}
			defined[v] = struct{}{}
		}
	}
}

// Sealed 报告分析器是否已经成功封口。
func (a *Analyzer) Sealed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sealed
}

// EntryID 返回第一个被成功录入的块编号（即入口块）。
func (a *Analyzer) EntryID() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.sealed {
		return 0, &AnalysisError{Code: ErrNotSealed, Detail: "analyzer is not sealed"}
	}
	return a.order[0], nil
}

// Block 查询单个块封口后的结果。
// 报错优先级：尚未封口 → 块不存在。
func (a *Analyzer) Block(id int) (BlockSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.sealed {
		return BlockSnapshot{}, &AnalysisError{Code: ErrNotSealed, Detail: "analyzer is not sealed"}
	}
	b, ok := a.blocks[id]
	if !ok {
		return BlockSnapshot{}, &AnalysisError{Code: ErrNoSuchBlock, Detail: fmt.Sprintf("block id %d does not exist", id)}
	}
	return snapshot(b), nil
}

// Blocks 返回全部块的快照，按块编号升序。
func (a *Analyzer) Blocks() ([]BlockSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.sealed {
		return nil, &AnalysisError{Code: ErrNotSealed, Detail: "analyzer is not sealed"}
	}
	out := make([]BlockSnapshot, 0, len(a.order))
	for _, id := range sortedInts(a.blocks) {
		out = append(out, snapshot(a.blocks[id]))
	}
	return out, nil
}

// EntryLiveIn 返回入口块入口活跃集，即可能使用未定义变量的集合。
func (a *Analyzer) EntryLiveIn() ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.sealed {
		return nil, &AnalysisError{Code: ErrNotSealed, Detail: "analyzer is not sealed"}
	}
	return sortedSet(a.blocks[a.order[0]].liveIn), nil
}

func snapshot(b *block) BlockSnapshot {
	return BlockSnapshot{
		ID:      b.id,
		UE:      sortedSet(b.ue),
		Def:     sortedSet(b.def),
		LiveIn:  sortedSet(b.liveIn),
		LiveOut: sortedSet(b.liveOut),
	}
}

func copyInstructions(in []Instruction) []Instruction {
	if len(in) == 0 {
		return nil
	}
	out := make([]Instruction, len(in))
	for i := range in {
		out[i].Uses = copyStrings(in[i].Uses)
		out[i].Defs = copyStrings(in[i].Defs)
	}
	return out
}

func copyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func copyInts(in []int) []int {
	if len(in) == 0 {
		return nil
	}
	out := make([]int, len(in))
	copy(out, in)
	return out
}

func sortedInts(m map[int]*block) []int {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func sortedSet(s map[string]struct{}) []string {
	if len(s) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func setEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for v := range a {
		if _, ok := b[v]; !ok {
			return false
		}
	}
	return true
}
