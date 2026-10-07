package snapshot

import "fmt"

// BoundaryRequest 描述一次边界位点请求。
// At 为 nil 时表示自动模式：取当前日志尖端之前最近的一个
// 不切分任何事务的安全位置；否则请求一个明确的边界位置
// （0 表示空快照，全部写入走增量）。
type BoundaryRequest struct {
	At *LSN
}

// At 构造一个显式边界请求。
func At(n LSN) BoundaryRequest {
	return BoundaryRequest{At: &n}
}

// Auto 构造一个自动边界请求。
func Auto() BoundaryRequest {
	return BoundaryRequest{}
}

// BoundaryDeterminer 负责确定快照的逻辑边界位点。边界一旦确定
// 就是一个具体的 LSN，不随后续写入变化。
type BoundaryDeterminer struct {
	journal *Journal
}

// NewBoundaryDeterminer 构造边界确定器。
func NewBoundaryDeterminer(j *Journal) *BoundaryDeterminer {
	return &BoundaryDeterminer{journal: j}
}

// Resolve 依据请求确定边界位置。
//
// 自动模式：返回不超过当前尖端的最大事务安全位置（可以为 0，
// 表示空快照）。显式模式：位置必须已存在（不超过当前尖端，
// 规则 R2），且不得切分事务（规则 R3），否则报边界定义冲突。
func (d *BoundaryDeterminer) Resolve(req BoundaryRequest) (LSN, error) {
	tip := d.journal.Tip()
	if req.At == nil {
		return d.lastTxnSafePoint(tip), nil
	}
	if *req.At > tip {
		return 0, &ExportError{
			Kind:   ErrBoundaryConflict,
			Rule:   RuleBoundaryExists,
			Detail: fmt.Sprintf("requested boundary %d beyond journal tip %d", *req.At, tip),
			LSN:    *req.At,
		}
	}
	if !d.isTxnSafe(*req.At) {
		return 0, &ExportError{
			Kind:   ErrBoundaryConflict,
			Rule:   RuleBoundaryTxnSafe,
			Detail: fmt.Sprintf("requested boundary %d splits a transaction", *req.At),
			LSN:    *req.At,
		}
	}
	return *req.At, nil
}

// isTxnSafe 报告位置 n 是否不切分任何事务：不存在一笔事务既有
// LSN<=n 的写入又有 LSN>n 的写入。
func (d *BoundaryDeterminer) isTxnSafe(n LSN) bool {
	tip := d.journal.Tip()
	if n >= tip {
		return true
	}
	before := map[TxnID]bool{}
	for _, w := range d.journal.Entries(0, n) {
		before[w.Txn] = true
	}
	for _, w := range d.journal.Entries(n, tip) {
		if before[w.Txn] {
			return false
		}
	}
	return true
}

// lastTxnSafePoint 返回不超过 tip 的最大事务安全位置。
func (d *BoundaryDeterminer) lastTxnSafePoint(tip LSN) LSN {
	// 位置 n 安全，当且仅当不存在事务 t 使 minLSN(t) <= n < maxLSN(t)，
	// 即 n 不落在任何事务的 [min, max) 区间内。从 tip 向下找：若 n 被
	// 某区间覆盖，则 [min, n] 全被该区间覆盖，可直接跳到 min-1。
	writes := d.journal.Entries(0, tip)
	lastOf := map[TxnID]LSN{}
	minOf := map[TxnID]LSN{}
	for _, w := range writes {
		lastOf[w.Txn] = w.LSN
		if cur, ok := minOf[w.Txn]; !ok || w.LSN < cur {
			minOf[w.Txn] = w.LSN
		}
	}
	n := tip
	for {
		// 找覆盖 n 的区间（min <= n < last），取最小的 min。
		var cover LSN
		found := false
		for txn, last := range lastOf {
			mn := minOf[txn]
			if mn <= n && n < last {
				if !found || mn < cover {
					cover = mn
					found = true
				}
			}
		}
		if !found {
			return n
		}
		if cover == 0 {
			return 0
		}
		n = cover - 1
	}
}
