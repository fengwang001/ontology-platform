package aria

// RunBatch 取最早的至多 bsz 个待处理事务执行一个批，返回每个事务的
// 阶段与最终 acc（失败者无 acc）。无待处理事务时返回空且不改状态。
func (e *Executor) RunBatch() []Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runBatchLocked()
}

func (e *Executor) runBatchLocked() []Result {
	n := e.bsz
	if len(e.pending) < n {
		n = len(e.pending)
	}
	if n == 0 {
		return nil
	}
	batch := make([]transaction, n)
	copy(batch, e.pending[:n])
	e.pending = e.pending[n:]

	results := make([]Result, n)

	// 第一阶段：每个事务在批起点快照上并行试执行。
	// 溢出失败者就此失败，不参与预留，也不回退。
	type trial struct {
		ok  bool
		acc int64
		buf map[int]int64
		rs  []int
		ws  []int
	}
	trials := make([]trial, n)
	snapshot := make([]int64, e.k)
	copy(snapshot, e.state)
	totalOps := 0
	for i, tx := range batch {
		totalOps += len(tx.ops)
		acc, buf, rs, ws, overflow := execOps(snapshot, tx.ops)
		if overflow {
			results[i] = Result{TxID: tx.id, Phase: PhaseFailed}
			continue
		}
		trials[i] = trial{ok: true, acc: acc, buf: buf, rs: rs, ws: ws}
	}

	// 第二阶段：按事务号升序建预留表并判定。
	// wres[k]/rres[k] 为 WS/RS 含 k 的最小事务号，中止者也计入。
	// 单趟升序扫描即可同时完成建表与判定：处理事务 t 时表中只可能有
	// 比 t 小的事务号，显式比较 < t 使判定与表项写入顺序无关。
	// 每个 WS/RS 键至多触及两个表项，总触及数不超过批内操作总数两倍。
	wres := make(map[int]uint64, totalOps)
	rres := make(map[int]uint64, totalOps)
	touches := 0
	committed := make([]bool, n)
	decisions := make([]decision, 0, n)
	for i, tx := range batch {
		tr := trials[i]
		if !tr.ok {
			continue
		}
		var waw, raw, war bool
		for _, k := range tr.ws {
			touches++
			if holder, ok := wres[k]; !ok {
				wres[k] = tx.id
			} else if holder < tx.id {
				waw = true
			}
			touches++
			if holder, ok := rres[k]; ok && holder < tx.id {
				war = true
			}
		}
		for _, k := range tr.rs {
			touches++
			if _, ok := rres[k]; !ok {
				rres[k] = tx.id
			}
			touches++
			if holder, ok := wres[k]; ok && holder < tx.id {
				raw = true
			}
		}
		committed[i] = !waw && !(raw && war)
		decisions = append(decisions, decision{id: tx.id, waw: waw, raw: raw, war: war, commit: committed[i]})
	}
	e.lastTouches = touches
	e.lastDecisions = decisions
	if touches > 2*totalOps {
		panic("aria: 预留表触及数超过批内操作总数两倍")
	}

	// 第三阶段：提交者的缓冲按事务号升序写入状态（写集两两不交，
	// 顺序无关）；其余按事务号升序在当前状态上串行重执行并立即写入，
	// 重执行溢出则失败。
	for i, tx := range batch {
		if !trials[i].ok || !committed[i] {
			continue
		}
		for k, v := range trials[i].buf {
			e.state[k] = v
		}
		results[i] = Result{TxID: tx.id, Phase: PhaseParallel, Acc: trials[i].acc, HasAcc: true}
	}
	for i, tx := range batch {
		if !trials[i].ok || committed[i] {
			continue
		}
		acc, buf, _, _, overflow := execOps(e.state, tx.ops)
		if overflow {
			results[i] = Result{TxID: tx.id, Phase: PhaseFailed}
			continue
		}
		for k, v := range buf {
			e.state[k] = v
		}
		results[i] = Result{TxID: tx.id, Phase: PhaseFallback, Acc: acc, HasAcc: true}
	}
	return results
}
