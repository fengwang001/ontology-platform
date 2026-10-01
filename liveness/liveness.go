// Package liveness 提供控制流图的活跃变量分析。
//
// 基本块可增量录入，后继允许前向引用尚未添加的块；
// 调用 Seal 封口后校验图完整性并按后向数据流方程计算
// 每块入口/出口的活跃变量集合（最小不动点），封口后结果不可变。
package liveness

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的错误原因，使用 errors.Is 判定。
var (
	// ErrNegativeBlockID 块编号为负。
	ErrNegativeBlockID = errors.New("liveness: negative block id")
	// ErrDuplicateBlock 块编号已存在。
	ErrDuplicateBlock = errors.New("liveness: duplicate block id")
	// ErrSealed 封口后仍尝试添加。
	ErrSealed = errors.New("liveness: analyzer already sealed")
	// ErrAlreadySealed 重复封口。
	ErrAlreadySealed = errors.New("liveness: seal called twice")
	// ErrNoBlocks 封口时没有任何块。
	ErrNoBlocks = errors.New("liveness: no blocks")
	// ErrMissingSuccessor 后继块不存在。
	ErrMissingSuccessor = errors.New("liveness: successor block not found")
	// ErrNotSealed 尚未封口就查询。
	ErrNotSealed = errors.New("liveness: not sealed yet")
	// ErrBlockNotFound 查询的块不存在。
	ErrBlockNotFound = errors.New("liveness: block not found")
)

// Instruction 表示一条指令的变量使用与定义。
// 同一条指令内先使用后定义，故 x = x + 1 中 x 同时属于 Uses 与 Defines。
type Instruction struct {
	Uses    []string
	Defines []string
}

// Analyzer 增量录入基本块并在封口后提供活跃变量查询。
// 所有方法均可并发调用，效果等价于某个串行顺序。
type Analyzer struct {
	mu      sync.RWMutex
	blocks  map[int]*block
	entry   int
	sealed  bool
	results map[int]blockResult
}

type block struct {
	id     int
	instrs []Instruction
	succs  []int
}

// blockResult 为封口后不可变的单块结果。
type blockResult struct {
	liveIn  []string
	liveOut []string
}

// NewAnalyzer 返回一个空的分析器。
func NewAnalyzer() *Analyzer {
	return &Analyzer{blocks: make(map[int]*block)}
}

// AddBlock 添加一个基本块。第一个添加的块为入口块。
// 按序校验：块编号为负、块编号已存在、封口后添加，只报第一个错误。
// 被拒绝时不改变已录入的块。
func (a *Analyzer) AddBlock(id int, instrs []Instruction, succs []int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if id < 0 {
		return fmt.Errorf("%w: %d", ErrNegativeBlockID, id)
	}
	if _, ok := a.blocks[id]; ok {
		return fmt.Errorf("%w: %d", ErrDuplicateBlock, id)
	}
	if a.sealed {
		return fmt.Errorf("%w: cannot add block %d", ErrSealed, id)
	}

	cp := &block{id: id, succs: append([]int(nil), succs...)}
	cp.instrs = make([]Instruction, len(instrs))
	for i, in := range instrs {
		cp.instrs[i] = Instruction{
			Uses:    append([]string(nil), in.Uses...),
			Defines: append([]string(nil), in.Defines...),
		}
	}
	if len(a.blocks) == 0 {
		a.entry = id
	}
	a.blocks[id] = cp
	return nil
}

// Seal 校验图并计算最小不动点。封口失败不改变已录入的块，可继续添加后再次封口。
// 按序校验：重复封口、没有任何块、后继块不存在
// （后继不存在按块编号升序、后继出现序报第一处）。
func (a *Analyzer) Seal() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.sealed {
		return ErrAlreadySealed
	}
	if len(a.blocks) == 0 {
		return ErrNoBlocks
	}
	ids := sortedIDs(a.blocks)
	for _, id := range ids {
		for _, s := range a.blocks[id].succs {
			if _, ok := a.blocks[s]; !ok {
				return fmt.Errorf("%w: block %d successor %d", ErrMissingSuccessor, id, s)
			}
		}
	}

	a.results = solve(a.blocks, ids)
	a.sealed = true
	return nil
}

// LiveIn 返回块 id 入口处的活跃变量集合（字典序）。
func (a *Analyzer) LiveIn(id int) ([]string, error) {
	res, err := a.resultOf(id)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), res.liveIn...), nil
}

// LiveOut 返回块 id 出口处的活跃变量集合（字典序）。
func (a *Analyzer) LiveOut(id int) ([]string, error) {
	res, err := a.resultOf(id)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), res.liveOut...), nil
}

// EntryLiveIn 返回入口块的入口活跃变量集合，即「可能使用未定义变量」的集合。
func (a *Analyzer) EntryLiveIn() ([]string, error) {
	a.mu.RLock()
	entry := a.entry
	a.mu.RUnlock()
	return a.LiveIn(entry)
}

func (a *Analyzer) resultOf(id int) (blockResult, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.sealed {
		return blockResult{}, ErrNotSealed
	}
	res, ok := a.results[id]
	if !ok {
		return blockResult{}, fmt.Errorf("%w: %d", ErrBlockNotFound, id)
	}
	return res, nil
}

func sortedIDs(blocks map[int]*block) []int {
	ids := make([]int, 0, len(blocks))
	for id := range blocks {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// ueDef 计算块的上行暴露使用集 UE 与定义集 Def。
// 指令按序执行，同一条指令内先使用后定义：
// 在块内此前未被定义就被使用的变量进入 UE，全部定义进入 Def。
func ueDef(instrs []Instruction) (ue, def map[string]bool) {
	ue = make(map[string]bool)
	def = make(map[string]bool)
	for _, in := range instrs {
		for _, u := range in.Uses {
			if !def[u] {
				ue[u] = true
			}
		}
		for _, d := range in.Defines {
			def[d] = true
		}
	}
	return ue, def
}

// solve 按后向数据流方程迭代至最小不动点：
//
//	LiveOut(b) = ∪ LiveIn(s)，s 为 b 的后继（无后继时为空集）
//	LiveIn(b)  = UE(b) ∪ (LiveOut(b) − Def(b))
//
// 从全空集合出发迭代，方程单调，故收敛到最小不动点。
// 结果按字典序排序，保证输出稳定、可精确复现。
func solve(blocks map[int]*block, ids []int) map[int]blockResult {
	ue := make(map[int]map[string]bool, len(ids))
	def := make(map[int]map[string]bool, len(ids))
	for _, id := range ids {
		ue[id], def[id] = ueDef(blocks[id].instrs)
	}

	liveIn := make(map[int]map[string]bool, len(ids))
	liveOut := make(map[int]map[string]bool, len(ids))
	for _, id := range ids {
		liveIn[id] = make(map[string]bool)
		liveOut[id] = make(map[string]bool)
	}

	for changed := true; changed; {
		changed = false
		for _, id := range ids {
			out := make(map[string]bool)
			for _, s := range blocks[id].succs {
				for v := range liveIn[s] {
					out[v] = true
				}
			}
			in := make(map[string]bool, len(ue[id])+len(out))
			for v := range ue[id] {
				in[v] = true
			}
			for v := range out {
				if !def[id][v] {
					in[v] = true
				}
			}
			if !equalSet(out, liveOut[id]) || !equalSet(in, liveIn[id]) {
				liveOut[id] = out
				liveIn[id] = in
				changed = true
			}
		}
	}

	results := make(map[int]blockResult, len(ids))
	for _, id := range ids {
		results[id] = blockResult{
			liveIn:  sortedSet(liveIn[id]),
			liveOut: sortedSet(liveOut[id]),
		}
	}
	return results
}

func equalSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for v := range a {
		if !b[v] {
			return false
		}
	}
	return true
}

func sortedSet(s map[string]bool) []string {
	out := make([]string, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
