package wallet

import (
	"sort"
	"sync"
)

// DeductItem 是一笔消费在单个批次上的扣减明细。
type DeductItem struct {
	BatchID int64
	Amount  int64
}

// RefundItem 是一笔退回在单个批次上的退回明细。
// Voided 为 true 表示该批在退回时刻已过期，退回数量作废（计入过期丢弃）。
type RefundItem struct {
	BatchID int64
	Amount  int64
	Voided  bool
}

// Reason 是操作被拒绝时的具体原因。
type Reason string

const (
	// ReasonNowRegression：now 小于此前任何操作的 now。
	ReasonNowRegression Reason = "now regression"
	// ReasonNonPositiveAmount：数量非正。
	ReasonNonPositiveAmount Reason = "non-positive amount"
	// ReasonDuplicateBatchID：批次 id 重复。
	ReasonDuplicateBatchID Reason = "duplicate batch id"
	// ReasonDuplicateSpendID：消费单 id 重复。
	ReasonDuplicateSpendID Reason = "duplicate spend id"
	// ReasonUnknownSpend：Refund 的消费单不存在。
	ReasonUnknownSpend Reason = "unknown spend"
	// ReasonRefundTooLarge：退回数量超过消费单尚未退回的总量。
	ReasonRefundTooLarge Reason = "refund too large"
	// ReasonInsufficientBalance：Spend 时有效余额不足。
	ReasonInsufficientBalance Reason = "insufficient balance"
	// ReasonGrantExpired：发放时 exp 不大于 now（发放即过期）。
	ReasonGrantExpired Reason = "grant already expired"
)

// Error 携带拒绝原因，便于调用方按原因判定。
type Error struct {
	Reason Reason
}

func (e *Error) Error() string { return "wallet: " + string(e.Reason) }

func errOf(r Reason) error { return &Error{Reason: r} }

// batch 是一个积分批次的内部状态。
type batch struct {
	id        int64
	exp       int64
	grantSeq  int64 // 发放先后序号（单调递增）
	remaining int64 // 当前剩余量（过期后保留旧值但不再计入余额）
	deducted  int64 // 累计已扣量
	refunded  int64 // 累计已退回量（含作废）
}

// deductEntry 记录某消费单在某批次上的扣减及后续退回情况。
type deductEntry struct {
	batchID  int64
	deducted int64 // 当初被该消费单扣走的数量
	refunded int64 // 该批上已经退回的数量（含作废）
}

// spend 是一张消费单。
type spend struct {
	entries []*deductEntry // 按消费时的扣减次序排列（先扣的在前）
}

func (s *spend) remainingRefund() int64 {
	var total int64
	for _, e := range s.entries {
		total += e.deducted - e.refunded
	}
	return total
}

// Wallet 是按到期时刻优先扣减的积分钱包。
type Wallet struct {
	mu sync.Mutex

	lastNow   int64
	grantSeq  int64
	batches   map[int64]*batch
	spends    map[int64]*spend
	discarded int64 // 累计过期丢弃数量
}

// New 创建一个空钱包。
func New() *Wallet {
	return &Wallet{
		batches: make(map[int64]*batch),
		spends:  make(map[int64]*spend),
	}
}

// Grant 发放一个带有效期的积分批次。
func (w *Wallet) Grant(id, amount, exp, now int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 拒绝顺序：now 回退 -> 数量非正 -> 批次 id 重复 -> 发放即过期。
	if now < w.lastNow {
		return errOf(ReasonNowRegression)
	}
	if amount <= 0 {
		return errOf(ReasonNonPositiveAmount)
	}
	if _, ok := w.batches[id]; ok {
		return errOf(ReasonDuplicateBatchID)
	}
	if exp <= now {
		return errOf(ReasonGrantExpired)
	}

	w.grantSeq++
	w.batches[id] = &batch{
		id:        id,
		exp:       exp,
		grantSeq:  w.grantSeq,
		remaining: amount,
	}
	w.lastNow = now
	return nil
}

// Spend 按到期先后消费积分，返回各批扣减明细并记在消费单 sid 名下。
func (w *Wallet) Spend(sid, amount, now int64) ([]DeductItem, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 拒绝顺序：now 回退 -> 数量非正 -> 消费单 id 重复 -> 有效余额不足。
	if now < w.lastNow {
		return nil, errOf(ReasonNowRegression)
	}
	if amount <= 0 {
		return nil, errOf(ReasonNonPositiveAmount)
	}
	if _, ok := w.spends[sid]; ok {
		return nil, errOf(ReasonDuplicateSpendID)
	}

	candidates := make([]*batch, 0)
	var available int64
	for _, b := range w.batches {
		if now < b.exp && b.remaining > 0 { // 有效期 [grant, exp)，now == exp 已过期
			candidates = append(candidates, b)
			available += b.remaining
		}
	}
	if available < amount {
		return nil, errOf(ReasonInsufficientBalance)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].exp != candidates[j].exp {
			return candidates[i].exp < candidates[j].exp // 到期时刻小者优先
		}
		return candidates[i].grantSeq < candidates[j].grantSeq // 到期相同时发放先者优先
	})

	need := amount
	sp := &spend{}
	items := make([]DeductItem, 0)
	for _, b := range candidates {
		if need == 0 {
			break
		}
		take := b.remaining
		if take > need {
			take = need
		}
		b.remaining -= take
		b.deducted += take
		need -= take
		entry := &deductEntry{batchID: b.id, deducted: take}
		sp.entries = append(sp.entries, entry)
		items = append(items, DeductItem{BatchID: b.id, Amount: take})
	}

	w.spends[sid] = sp
	w.lastNow = now
	return items, nil
}

// Refund 退回消费单 sid 的部分已扣数量，返回各批退回明细。
func (w *Wallet) Refund(sid, amount, now int64) ([]RefundItem, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 拒绝顺序：now 回退 -> 数量非正 -> 消费单不存在 -> 退回超量。
	if now < w.lastNow {
		return nil, errOf(ReasonNowRegression)
	}
	if amount <= 0 {
		return nil, errOf(ReasonNonPositiveAmount)
	}
	sp, ok := w.spends[sid]
	if !ok {
		return nil, errOf(ReasonUnknownSpend)
	}
	if amount > sp.remainingRefund() {
		return nil, errOf(ReasonRefundTooLarge)
	}

	items := make([]RefundItem, 0)
	need := amount
	// 与扣减次序相反：从最后扣的批次退起。
	for i := len(sp.entries) - 1; i >= 0 && need > 0; i-- {
		entry := sp.entries[i]
		refundable := entry.deducted - entry.refunded
		if refundable <= 0 {
			continue
		}
		take := refundable
		if take > need {
			take = need
		}
		b := w.batches[entry.batchID]
		voided := now >= b.exp // 该批在 now 时已过期（含 now == exp）
		entry.refunded += take
		b.refunded += take
		// 无论是否过期都退回批次剩余桶：过期批的剩余与自然过期未用完的部分
		// 一样不计入有效余额、不可再消费，但保证 R+D-F=发放量 的恒等式成立。
		b.remaining += take
		if voided {
			// 作废：不增加有效余额，计入过期丢弃。
			w.discarded += take
		}
		need -= take
		items = append(items, RefundItem{BatchID: b.id, Amount: take, Voided: voided})
	}

	w.lastNow = now
	return items, nil
}

// Balance 返回 now 时有效批次的剩余总和。
func (w *Wallet) Balance(now int64) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 查询同样要求 now 不回退，并推进时间线（被拒绝的查询不改时间线）。
	if now < w.lastNow {
		return 0
	}
	var total int64
	for _, b := range w.batches {
		if now < b.exp {
			total += b.remaining
		}
	}
	w.lastNow = now
	return total
}

// BatchState 是一个批次的内部状态快照，用于校验与诊断。
type BatchState struct {
	ID        int64
	Exp       int64
	Granted   int64
	Remaining int64
	Deducted  int64
	Refunded  int64
	Expired   bool
}

// Batches 返回所有批次按 id 排序的状态快照。
func (w *Wallet) Batches(now int64) []BatchState {
	w.mu.Lock()
	defer w.mu.Unlock()

	ids := make([]int64, 0, len(w.batches))
	for id := range w.batches {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]BatchState, 0, len(ids))
	for _, id := range ids {
		b := w.batches[id]
		out = append(out, BatchState{
			ID:        b.id,
			Exp:       b.exp,
			Granted:   b.remaining + b.deducted - b.refunded,
			Remaining: b.remaining,
			Deducted:  b.deducted,
			Refunded:  b.refunded,
			Expired:   now >= b.exp,
		})
	}
	return out
}

// Discarded 返回截至目前因退回时批次已过期而作废的累计数量。
func (w *Wallet) Discarded() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.discarded
}
