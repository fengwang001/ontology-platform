package ontology

import "sort"

// naiveModel 是按题目规则逐步写成的朴素模拟：
// 提交扫描直接线性遍历整张事务表，visited 也按规则独立统计，
// 不借助 Registry 内部的 ready 索引，因此能对其形成独立对照。
type modelEntry struct {
	state TxnState
	epoch int
	n     int64
	life  int64
}

type naiveModel struct {
	p, m       int
	lb, ln     int64
	life       int64
	txns       map[TxnName]*modelEntry
	committedN int64
	abortedN   int64
	probes     int64
	visited    int64
}

func newNaiveModel(P, M int) *naiveModel {
	return &naiveModel{p: P, m: M, txns: map[TxnName]*modelEntry{}}
}

func (mdl *naiveModel) sortedPending() []TxnName {
	out := make([]TxnName, 0)
	for name, e := range mdl.txns {
		if e.state == OPEN || e.state == PREPARED {
			out = append(out, name)
		}
	}
	sortNames(out)
	return out
}

func (mdl *naiveModel) Write(s int, n int64) error {
	if s < 0 || s >= mdl.p || n < 1 || n > 1_000_000 {
		return ErrInvalidArgument
	}
	name := TxnName{S: s, K: mdl.lb + 1}
	old, ok := mdl.txns[name]
	if ok && old.state == OPEN && old.life == mdl.life {
		old.n += n
		return nil
	}
	epoch := 0
	if ok {
		epoch = old.epoch + 1
		if old.state == OPEN || old.state == PREPARED {
			old.state = ABORTED
			mdl.abortedN += old.n
		}
	}
	mdl.txns[name] = &modelEntry{state: OPEN, epoch: epoch, n: n, life: mdl.life}
	return nil
}

func (mdl *naiveModel) Barrier(cp int64) ([]TxnName, error) {
	if cp < 1 {
		return nil, ErrInvalidArgument
	}
	if cp != mdl.lb+1 {
		return nil, ErrStaleBarrier
	}
	out := make([]TxnName, 0)
	for s := 0; s < mdl.p; s++ {
		name := TxnName{S: s, K: cp}
		if e, ok := mdl.txns[name]; ok && e.state == OPEN && e.life == mdl.life {
			e.state = PREPARED
			out = append(out, name)
		}
	}
	mdl.lb = cp
	return out, nil
}

// commitUpTo 朴素实现：每次全表扫描，按 (k,s) 排序后逐个提交。
// visited 独立统计：对所有当前 life 的 PREPARED 条目按序访问，
// 访问到第一个 k>c 的待提交条目即停（再计一次），全部可提交时不多计。
func (mdl *naiveModel) commitUpTo(c int64) []TxnName {
	type cand struct {
		name  TxnName
		entry *modelEntry
	}
	cands := make([]cand, 0)
	for name, e := range mdl.txns {
		if e.state == PREPARED && e.life == mdl.life {
			cands = append(cands, cand{name, e})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].name.K != cands[j].name.K {
			return cands[i].name.K < cands[j].name.K
		}
		return cands[i].name.S < cands[j].name.S
	})
	out := make([]TxnName, 0)
	for _, cd := range cands {
		mdl.visited++
		if cd.name.K > c {
			break
		}
		cd.entry.state = COMMITTED
		mdl.committedN += cd.entry.n
		out = append(out, cd.name)
	}
	return out
}

func (mdl *naiveModel) Complete(c int64) ([]TxnName, error) {
	if c < 1 {
		return nil, ErrInvalidArgument
	}
	if c <= mdl.ln {
		return nil, ErrStaleNotice
	}
	if c > mdl.lb {
		return nil, ErrFutureNotice
	}
	out := mdl.commitUpTo(c)
	mdl.ln = c
	return out, nil
}

func (mdl *naiveModel) Restore(c int64, newP int) (RestoreResult, error) {
	if c < 0 || newP < 1 || newP > 64 {
		return RestoreResult{}, ErrInvalidArgument
	}
	if c < mdl.ln {
		return RestoreResult{}, ErrRestoreFallback
	}
	if c > mdl.lb {
		return RestoreResult{}, ErrRestoreAhead
	}
	res := RestoreResult{Committed: mdl.commitUpTo(c)}

	maxP := mdl.p
	if newP > maxP {
		maxP = newP
	}
	for s := 0; s < maxP; s++ {
		missing := 0
		for k := c + 1; missing < mdl.m; k++ {
			name := TxnName{S: s, K: k}
			mdl.probes++
			res.Probes++
			if e, ok := mdl.txns[name]; ok && (e.state == OPEN || e.state == PREPARED) {
				e.state = ABORTED
				mdl.abortedN += e.n
				res.Aborted = append(res.Aborted, name)
				missing = 0
			} else {
				missing++
			}
		}
	}
	mdl.p = newP
	mdl.lb = c
	mdl.ln = c
	mdl.life++
	return res, nil
}

func (mdl *naiveModel) Txn(s int, k int64) (TxnInfo, error) {
	e, ok := mdl.txns[TxnName{S: s, K: k}]
	if !ok {
		return TxnInfo{}, ErrNoSuchTxn
	}
	return TxnInfo{State: e.state, Epoch: e.epoch, N: e.n}, nil
}

type modelStats struct {
	committed, aborted, open, prepared int64
	probes, visited                    int64
}

func (mdl *naiveModel) stats() modelStats {
	st := modelStats{committed: mdl.committedN, aborted: mdl.abortedN, probes: mdl.probes, visited: mdl.visited}
	for _, e := range mdl.txns {
		switch e.state {
		case OPEN:
			st.open += e.n
		case PREPARED:
			st.prepared += e.n
		}
	}
	return st
}

// dump 返回可比较的完整内部状态（事务表 + 计数器 + 水位）。
func (mdl *naiveModel) dump() map[string]any {
	txns := make(map[TxnName]TxnInfo, len(mdl.txns))
	for name, e := range mdl.txns {
		txns[name] = TxnInfo{State: e.state, Epoch: e.epoch, N: e.n}
	}
	st := mdl.stats()
	return map[string]any{
		"p": mdl.p, "lb": mdl.lb, "ln": mdl.ln, "life": mdl.life,
		"txns": txns, "stats": st,
	}
}
