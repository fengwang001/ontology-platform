// Package naive 是批量导入子系统的朴素参照实现。
//
// 它与 importer.Engine 独立实现：缓冲所有到达的块，等待调用方显式
// 触发 Finalize 后，按序号顺序汇总条目，用反复全表扫描的不动点算法
// 统一判定结果。它用于与引擎对拍：给定相同的块到达序列（含乱序与
// 重复注入），两者的最终条目状态必须完全一致。
package naive

import (
	"fmt"
	"sort"

	"ontology/importer"
)

// Model 缓冲一个任务到达的所有块。
type Model struct {
	maxChunks int
	validator importer.Validator
	chunks    map[int]chunkRec
	conflicts []importer.Chunk
}

type chunkRec struct {
	hash  [32]byte
	chunk importer.Chunk
}

// New 创建朴素模型。maxChunks 语义与引擎一致：0 表示不声明上限。
func New(maxChunks int, validator importer.Validator) *Model {
	return &Model{
		maxChunks: maxChunks,
		validator: validator,
		chunks:    make(map[int]chunkRec),
	}
}

// Submit 缓冲一个块。同一序号首次到达的内容生效；内容一致的重复到达
// 被忽略；内容不一致的重复到达被记录为冲突，其中条目不参与处理。
func (m *Model) Submit(c importer.Chunk) {
	sum := c.Hash()
	if rec, ok := m.chunks[c.Seq]; ok {
		if rec.hash != sum {
			m.conflicts = append(m.conflicts, c)
		}
		return
	}
	m.chunks[c.Seq] = chunkRec{hash: sum, chunk: c}
}

type entryState struct {
	entry importer.Entry
	info  importer.EntryInfo
}

// Finalize 等待全部块到齐后统一处理，返回每个已知条目的最终状态。
func (m *Model) Finalize() map[string]importer.EntryInfo {
	states := make(map[string]*entryState)
	seqs := make([]int, 0, len(m.chunks))
	for seq := range m.chunks {
		seqs = append(seqs, seq)
	}
	sort.Ints(seqs)
	for _, seq := range seqs {
		for _, ent := range m.chunks[seq].chunk.Entries {
			if _, ok := states[ent.ID]; ok {
				continue
			}
			states[ent.ID] = &entryState{
				entry: ent,
				info:  importer.EntryInfo{Status: importer.StatusPending},
			}
		}
	}
	for _, c := range m.conflicts {
		for _, ent := range c.Entries {
			if _, ok := states[ent.ID]; !ok {
				states[ent.ID] = &entryState{
					entry: ent,
					info: importer.EntryInfo{
						Status:   importer.StatusConflict,
						Category: importer.CategoryChunkConflict,
						Detail:   "first seen in conflicting duplicate chunk",
					},
				}
			}
		}
	}

	// 不动点传播：引用全部落地则应用校验结果，引用已失败则传播失败。
	propagate(states, m.validator)
	// 循环引用：全部引用已知却仍悬挂的条目只可能处于环中。
	// 只判定汇点环（规范语义见设计文档），环上游条目通过传播失败判定。
	for breakSinkCycles(states) {
		propagate(states, m.validator)
	}
	// 达到块数上限：仍存在未知引用的悬挂条目判定超时，并沿引用传播。
	if m.maxChunks > 0 && len(m.chunks) >= m.maxChunks {
		for {
			marked := false
			for _, s := range states {
				if s.info.Status == importer.StatusPending && hasUnknownRef(s, states) {
					s.info = importer.EntryInfo{
						Status:   importer.StatusTimeout,
						Category: importer.CategoryDanglingTimeout,
						Detail:   "dangling references remain after all chunks arrived",
					}
					marked = true
				}
			}
			propagate(states, m.validator)
			if !marked {
				break
			}
		}
		for breakSinkCycles(states) {
			propagate(states, m.validator)
		}
	}

	result := make(map[string]importer.EntryInfo, len(states))
	for id, s := range states {
		result[id] = s.info
	}
	return result
}

func isTerminalFailed(st importer.EntryStatus) bool {
	return st == importer.StatusFailed || st == importer.StatusTimeout || st == importer.StatusConflict
}

func hasUnknownRef(s *entryState, states map[string]*entryState) bool {
	for _, ref := range s.entry.References {
		if _, ok := states[ref]; !ok {
			return true
		}
	}
	return false
}

// propagate 反复全表扫描直到没有状态变化，是朴素模型的核心：
// 它的开销随条目总数增长，这正是引擎要避免的。
func propagate(states map[string]*entryState, validator importer.Validator) {
	for {
		progress := false
		for _, s := range states {
			if s.info.Status != importer.StatusPending {
				continue
			}
			failedRef := ""
			waiting := false
			for _, ref := range s.entry.References {
				t, ok := states[ref]
				if !ok {
					waiting = true
					continue
				}
				switch {
				case t.info.Status == importer.StatusLanded:
				case t.info.Status == importer.StatusPending:
					waiting = true
				case isTerminalFailed(t.info.Status):
					failedRef = ref
				}
				if failedRef != "" {
					break
				}
			}
			if failedRef != "" {
				s.info = importer.EntryInfo{
					Status:   importer.StatusFailed,
					Category: importer.CategoryReferenceFailed,
					Detail:   fmt.Sprintf("referenced entry %q already failed", failedRef),
				}
				progress = true
				continue
			}
			if waiting {
				continue
			}
			if err := validator(s.entry); err != nil {
				s.info = importer.EntryInfo{
					Status:   importer.StatusFailed,
					Category: importer.CategoryEntryInvalid,
					Detail:   err.Error(),
				}
			} else {
				s.info = importer.EntryInfo{Status: importer.StatusLanded}
			}
			progress = true
		}
		if !progress {
			return
		}
	}
}

// breakSinkCycles 在悬挂条目构成的依赖图中找出所有“封闭”的环并判定
// 失败（按条目校验失败类别），找到并处理至少一个环即返回 true。
//
// 封闭环规则（与引擎共享的规范语义）：环内每个条目的每个引用要么已
// 落地、要么在环内。满足该条件时不会再有任何未来事件改变判定依据；
// 封闭的环互不可达，判定顺序无关。
func breakSinkCycles(states map[string]*entryState) bool {
	pending := make(map[string]*entryState)
	for id, s := range states {
		if s.info.Status == importer.StatusPending {
			pending[id] = s
		}
	}
	if len(pending) == 0 {
		return false
	}
	// Tarjan 强连通分量，边为指向悬挂条目的引用。
	index := make(map[string]int, len(pending))
	lowlink := make(map[string]int, len(pending))
	onStack := make(map[string]bool, len(pending))
	var stack []string
	var sccs [][]string
	counter := 0
	var strongConnect func(v string)
	strongConnect = func(v string) {
		index[v] = counter
		lowlink[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, ref := range pending[v].entry.References {
			if _, ok := pending[ref]; !ok {
				continue
			}
			if _, visited := index[ref]; !visited {
				strongConnect(ref)
				if lowlink[ref] < lowlink[v] {
					lowlink[v] = lowlink[ref]
				}
			} else if onStack[ref] {
				if index[ref] < lowlink[v] {
					lowlink[v] = index[ref]
				}
			}
		}
		if lowlink[v] == index[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, visited := index[id]; !visited {
			strongConnect(id)
		}
	}
	broke := false
	for _, scc := range sccs {
		cyclic := len(scc) > 1
		if len(scc) == 1 {
			for _, ref := range pending[scc[0]].entry.References {
				if ref == scc[0] {
					cyclic = true
				}
			}
		}
		if !cyclic || !closedCycle(scc, pending, states) {
			continue
		}
		sort.Strings(scc)
		for _, id := range scc {
			states[id].info = importer.EntryInfo{
				Status:   importer.StatusFailed,
				Category: importer.CategoryEntryInvalid,
				Detail:   fmt.Sprintf("circular reference among %v", scc),
			}
		}
		broke = true
	}
	return broke
}

// closedCycle 判断环是否封闭：环内每个条目的每个引用要么已落地、要么在环内。
func closedCycle(scc []string, pending, states map[string]*entryState) bool {
	members := make(map[string]bool, len(scc))
	for _, id := range scc {
		members[id] = true
	}
	for _, id := range scc {
		for _, ref := range pending[id].entry.References {
			target, ok := states[ref]
			if !ok {
				return false
			}
			if target.info.Status == importer.StatusLanded {
				continue
			}
			if target.info.Status == importer.StatusPending && members[ref] {
				continue
			}
			return false
		}
	}
	return true
}
