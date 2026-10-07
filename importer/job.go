package importer

import (
	"fmt"
	"log/slog"
	"sync"
)

// entryState 是条目在任务内的处理状态。
type entryState struct {
	entry    Entry
	status   EntryStatus
	category Category
	detail   string
	valErr   error
}

// chunkRecord 记录已受理序号的内容摘要，用于重复到达判定。
type chunkRecord struct {
	hash [32]byte
}

// job 保存一个导入任务的全部状态。
//
// 悬挂引用的记录与查找只依赖两个索引：
//   - pending：当前仍处于悬挂状态的条目集合；
//   - waiters：被引用条目 ID -> 等待它的悬挂条目集合。
//
// 解析过程是事件驱动的：条目转为终态时只唤醒 waiters 中直接等待它的
// 条目，环检测与超时清扫只扫描 pending 集合，均不遍历已处理条目，
// 因此开销只随当前悬挂引用数量增长，与已处理条目总数无关。
type job struct {
	id        string
	maxChunks int
	validator Validator
	log       *slog.Logger

	mu         sync.RWMutex
	terminated bool
	chunks     map[int]chunkRecord
	arrived    int
	// inflight 记录已受理但尚未提交完成的块数量。达到块数上限的
	// 超时清扫必须等所有已受理块提交完毕，保证并发提交的结果与
	// 某个串行顺序等价。
	inflight int
	entries  map[string]*entryState
	pending  map[string]*entryState
	waiters  map[string]map[string]*entryState

	landed   int
	failed   int
	timedOut int
	conflict int
	evalOps  int64
	scanOps  int64
}

func newJob(id string, maxChunks int, v Validator, log *slog.Logger) *job {
	return &job{
		id:        id,
		maxChunks: maxChunks,
		validator: v,
		log:       log,
		chunks:    make(map[int]chunkRecord),
		entries:   make(map[string]*entryState),
		pending:   make(map[string]*entryState),
		waiters:   make(map[string]map[string]*entryState),
	}
}

// markConflictLocked 处理内容不一致的重复块：已确定结果的条目保持不变，
// 首次出现于该冲突块中的条目被标记为 StatusConflict。
func (j *job) markConflictLocked(c Chunk) int {
	marked := 0
	for _, ent := range c.Entries {
		if _, known := j.entries[ent.ID]; known {
			continue
		}
		j.entries[ent.ID] = &entryState{
			entry:    ent,
			status:   StatusConflict,
			category: CategoryChunkConflict,
			detail:   fmt.Sprintf("first seen in conflicting duplicate chunk seq %d", c.Seq),
		}
		j.conflict++
		marked++
	}
	return marked
}

// commitLocked 提交一个已受理的块：注册条目、解析悬挂引用、
// 检测循环引用，并在达到块数上限时执行超时清扫。
func (j *job) commitLocked(c Chunk, valErrs []error) {
	j.log.Info("chunk accepted", "job", j.id, "seq", c.Seq,
		"entries", len(c.Entries), "hash", fmt.Sprintf("%x", c.Hash())[:12])
	var queue []*entryState
	for i, ent := range c.Entries {
		if _, dup := j.entries[ent.ID]; dup {
			j.log.Warn("entry id already known, first occurrence wins",
				"job", j.id, "seq", c.Seq, "entry", ent.ID)
			continue
		}
		es := &entryState{entry: ent, status: StatusPending, valErr: valErrs[i]}
		j.entries[ent.ID] = es
		j.pending[ent.ID] = es
		j.evalLocked(es, &queue)
	}
	j.drainLocked(&queue)
	j.breakCyclesLocked(&queue)
	j.sweepTimeoutLocked(&queue)
}

// evalLocked 评估一个悬挂条目：引用已失败则立即传播失败；引用全部
// 落地则应用校验结果；否则登记到 waiters 索引继续等待。
func (j *job) evalLocked(es *entryState, queue *[]*entryState) {
	j.evalOps++
	var failedRef *entryState
	var waitOn []string
	for _, ref := range es.entry.References {
		target, ok := j.entries[ref]
		if !ok {
			waitOn = append(waitOn, ref)
			continue
		}
		switch target.status {
		case StatusLanded:
		case StatusPending:
			waitOn = append(waitOn, ref)
		default:
			// StatusFailed / StatusTimeout / StatusConflict 均为已确定失败。
			failedRef = target
		}
		if failedRef != nil {
			break
		}
	}
	if failedRef != nil {
		j.failLocked(es, CategoryReferenceFailed,
			fmt.Sprintf("referenced entry %q already terminated as %s",
				failedRef.entry.ID, failedRef.status), queue)
		return
	}
	if len(waitOn) > 0 {
		for _, ref := range waitOn {
			j.addWaiterLocked(ref, es)
		}
		j.log.Info("entry pending", "job", j.id, "entry", es.entry.ID,
			"reason", "waiting on dangling references", "waiting_on", waitOn)
		return
	}
	if es.valErr != nil {
		j.failLocked(es, CategoryEntryInvalid, es.valErr.Error(), queue)
		return
	}
	j.landLocked(es, queue)
}

// drainLocked 处理终态事件队列：条目转为终态时，只唤醒 waiters 索引中
// 直接等待它的悬挂条目，不扫描其他条目。
func (j *job) drainLocked(queue *[]*entryState) {
	for len(*queue) > 0 {
		es := (*queue)[0]
		*queue = (*queue)[1:]
		set := j.waiters[es.entry.ID]
		if len(set) == 0 {
			continue
		}
		delete(j.waiters, es.entry.ID)
		for _, w := range set {
			if w.status == StatusPending {
				j.evalLocked(w, queue)
			}
		}
	}
}

// breakCyclesLocked 检测悬挂条目之间的循环引用并判定失败。
//
// 判定规则（与朴素参照模型共享的规范语义）：在悬挂条目构成的依赖图中，
// 一个环（强连通分量）只有在“封闭”时才能被判定为循环引用失败——即环内
// 每个条目的每个引用要么已落地、要么在环内。此时不会再有任何未来事件
// 改变这些条目的判定依据；反之，只要环内条目还引用着环外尚未确定的条目，
// 它就可能因传播失败（优先级 2）而非循环（优先级 4）被判定，必须等待。
// 封闭的环之间互不可达，判定顺序无关，结果与遍历顺序无关。
func (j *job) breakCyclesLocked(queue *[]*entryState) {
	for {
		if len(j.pending) == 0 {
			return
		}
		j.scanOps += int64(len(j.pending))
		broke := false
		for _, scc := range tarjan(j.pending) {
			if len(scc) == 1 && !hasSelfLoop(scc[0]) {
				continue
			}
			if !j.closedCycleLocked(scc) {
				continue
			}
			ids := make([]string, 0, len(scc))
			for _, es := range scc {
				ids = append(ids, es.entry.ID)
			}
			j.log.Warn("circular reference detected", "job", j.id, "entries", ids,
				"reason", "dangling entries form a closed cycle; cycle can never resolve")
			for _, es := range scc {
				j.failLocked(es, CategoryEntryInvalid,
					fmt.Sprintf("circular reference among %v", ids), queue)
			}
			broke = true
		}
		j.drainLocked(queue)
		if !broke {
			return
		}
	}
}

// closedCycleLocked 判断环是否封闭：环内每个条目的每个引用要么已落地，
// 要么在环内。
func (j *job) closedCycleLocked(scc []*entryState) bool {
	members := make(map[string]bool, len(scc))
	for _, es := range scc {
		members[es.entry.ID] = true
	}
	for _, es := range scc {
		for _, ref := range es.entry.References {
			target, ok := j.entries[ref]
			if !ok {
				return false
			}
			if target.status == StatusLanded {
				continue
			}
			if target.status == StatusPending && members[ref] {
				continue
			}
			return false
		}
	}
	return true
}

// sweepTimeoutLocked 在已到达块数达到声明上限时，把仍存在未知引用的
// 悬挂条目判定为超时失败，并沿引用传播。
//
// 只有当一个已受理块是最后一个在途提交（inflight==1）时才执行清扫，
// 保证并发受理的块全部提交后才判定，结果与串行顺序等价。
func (j *job) sweepTimeoutLocked(queue *[]*entryState) {
	if j.maxChunks <= 0 || j.arrived < j.maxChunks || j.inflight != 1 {
		return
	}
	for {
		marked := false
		for _, es := range j.pending {
			j.scanOps++
			if j.hasUnknownRefLocked(es) {
				j.timeoutLocked(es, queue)
				marked = true
			}
		}
		j.drainLocked(queue)
		if !marked {
			break
		}
	}
	// 超时判定后仍悬挂的条目引用全部已知，只可能处于循环中。
	j.breakCyclesLocked(queue)
}

func (j *job) hasUnknownRefLocked(es *entryState) bool {
	for _, ref := range es.entry.References {
		if _, ok := j.entries[ref]; !ok {
			return true
		}
	}
	return false
}

func (j *job) addWaiterLocked(ref string, es *entryState) {
	set, ok := j.waiters[ref]
	if !ok {
		set = make(map[string]*entryState)
		j.waiters[ref] = set
	}
	set[es.entry.ID] = es
}

// removeWaitersLocked 在条目离开悬挂状态时，清除它登记的所有等待边，
// 保证 waiters 索引恰好等于当前悬挂引用集合。
func (j *job) removeWaitersLocked(es *entryState) {
	for _, ref := range es.entry.References {
		set, ok := j.waiters[ref]
		if !ok {
			continue
		}
		delete(set, es.entry.ID)
		if len(set) == 0 {
			delete(j.waiters, ref)
		}
	}
}

func (j *job) landLocked(es *entryState, queue *[]*entryState) {
	es.status = StatusLanded
	delete(j.pending, es.entry.ID)
	j.removeWaitersLocked(es)
	j.landed++
	*queue = append(*queue, es)
	j.log.Info("entry landed", "job", j.id, "entry", es.entry.ID,
		"reason", "all references landed and validation passed")
}

func (j *job) failLocked(es *entryState, cat Category, detail string, queue *[]*entryState) {
	es.status = StatusFailed
	es.category = cat
	es.detail = detail
	delete(j.pending, es.entry.ID)
	j.removeWaitersLocked(es)
	j.failed++
	*queue = append(*queue, es)
	j.log.Warn("entry failed", "job", j.id, "entry", es.entry.ID,
		"category", cat.String(), "reason", detail)
}

func (j *job) timeoutLocked(es *entryState, queue *[]*entryState) {
	es.status = StatusTimeout
	es.category = CategoryDanglingTimeout
	es.detail = fmt.Sprintf("dangling references remain after %d chunks arrived", j.arrived)
	delete(j.pending, es.entry.ID)
	j.removeWaitersLocked(es)
	j.timedOut++
	*queue = append(*queue, es)
	j.log.Warn("entry timed out", "job", j.id, "entry", es.entry.ID,
		"category", CategoryDanglingTimeout.String(), "reason", es.detail)
}

func hasSelfLoop(es *entryState) bool {
	for _, ref := range es.entry.References {
		if ref == es.entry.ID {
			return true
		}
	}
	return false
}

// tarjan 在悬挂条目集合导出的子图上计算强连通分量，边为指向悬挂条目的引用。
// 只访问悬挂集合，规模随悬挂条目数量增长。
func tarjan(pending map[string]*entryState) [][]*entryState {
	index := make(map[*entryState]int, len(pending))
	lowlink := make(map[*entryState]int, len(pending))
	onStack := make(map[*entryState]bool, len(pending))
	roots := make([]*entryState, 0, len(pending))
	for _, es := range pending {
		roots = append(roots, es)
	}
	var stack []*entryState
	var sccs [][]*entryState
	counter := 0

	var strongConnect func(v *entryState)
	strongConnect = func(v *entryState) {
		index[v] = counter
		lowlink[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, ref := range v.entry.References {
			w, ok := pending[ref]
			if !ok {
				continue
			}
			if _, visited := index[w]; !visited {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if index[w] < lowlink[v] {
					lowlink[v] = index[w]
				}
			}
		}
		if lowlink[v] == index[v] {
			var scc []*entryState
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
	for _, es := range roots {
		if _, visited := index[es]; !visited {
			strongConnect(es)
		}
	}
	return sccs
}
