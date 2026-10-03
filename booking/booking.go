// Package booking 实现带超订比例、定向交叉可行性校验与候补顺位的
// 合约广告库存预订簿。
package booking

import "errors"

// 拒绝原因，按固定优先级返回，调用方可通过 errors.Is 精确区分。
var (
	ErrInvalid       = errors.New("invalid argument")
	ErrAlreadyExists = errors.New("contract already exists")
	ErrNotFound      = errors.New("contract not found")
	ErrNotBooked     = errors.New("contract is not booked (waiting)")
	ErrNeverBookable = errors.New("quantity can never fit its targeting mask")
	ErrInfeasible    = errors.New("infeasible with currently booked contracts")
)

// Contract 是一份合约的状态快照。
type Contract struct {
	ID   string
	Mask uint32
	Qty  int64
}

// Phase 表示合约所处阶段。
type Phase int

const (
	PhaseBooked  Phase = 1
	PhaseWaiting Phase = 2
)

// Result 是一次变更操作的可复现结果与判定依据。
type Result struct {
	OK             bool
	Reason         error
	Phase          Phase
	SubsetsChecked int
	Promoted       []string
	Booked         []Contract
	Waiting        []Contract
}

// Book 是库存预订簿。所有方法可并发调用，等价于某一串行顺序。
type Book struct {
	st *bookState
}

// Book 预订或进入候补。
func (b *Book) Book(id string, mask uint32, qty int64) Result {
	return b.st.doBook(id, mask, qty)
}

// Cancel 取消合约并在命中已预订时触发递补。
func (b *Book) Cancel(id string) Result {
	return b.st.doCancel(id)
}

// Resize 修改已预订合约数量。
func (b *Book) Resize(id string, q2 int64) Result {
	return b.st.doResize(id, q2)
}

// Retarget 修改已预订合约定向。
func (b *Book) Retarget(id string, mask2 uint32) Result {
	return b.st.doRetarget(id, mask2)
}

// BookedContracts 返回已预订合约快照（按 id 排序，便于复现对比）。
func (b *Book) BookedContracts() []Contract {
	b.st.mu.Lock()
	defer b.st.mu.Unlock()
	out := make([]Contract, 0, len(b.st.booked))
	for _, e := range b.st.booked {
		out = append(out, Contract{ID: e.id, Mask: e.mask, Qty: e.qty})
	}
	sortContracts(out)
	return out
}

// WaitingContracts 返回候补合约快照（严格保持到达顺序）。
func (b *Book) WaitingContracts() []Contract {
	b.st.mu.Lock()
	defer b.st.mu.Unlock()
	out := make([]Contract, 0, len(b.st.waiting))
	for _, e := range b.st.waiting {
		out = append(out, Contract{ID: e.id, Mask: e.mask, Qty: e.qty})
	}
	return out
}

// Capacity 返回非空子集 T 的容量 Cap(T)=floor(rho*S(T)/100)。
func (b *Book) Capacity(mask uint32) int64 {
	if mask == 0 || int(mask) >= len(b.st.capacity) {
		return -1
	}
	return b.st.capacity[mask]
}

// Feasible 返回当前已预订集合是否可行，枚举全部 2^C-1 个非空子集。
// 该查询不计入决策子集计数器。
func (b *Book) Feasible() bool {
	b.st.mu.Lock()
	defer b.st.mu.Unlock()
	for t := 1; t < len(b.st.capacity); t++ {
		var load int64
		for _, e := range b.st.booked {
			if e.mask&uint32(t) == e.mask {
				load += e.qty
			}
		}
		if load > b.st.capacity[t] {
			return false
		}
	}
	return true
}

// ResetCounters 重置决策可行性判定的枚举计数器。
func (b *Book) ResetCounters() {
	b.st.mu.Lock()
	defer b.st.mu.Unlock()
	b.st.counterSubsets = 0
	b.st.counterCalls = 0
}

// CounterSubsets 返回自上次重置以来决策判定枚举的子集总数
// （不含永不可订的单点判定与 Feasible 全量查询）。
func (b *Book) CounterSubsets() int64 {
	b.st.mu.Lock()
	defer b.st.mu.Unlock()
	return b.st.counterSubsets
}

// CounterCalls 返回自上次重置以来决策可行性流程的次数。
func (b *Book) CounterCalls() int64 {
	b.st.mu.Lock()
	defer b.st.mu.Unlock()
	return b.st.counterCalls
}

func sortContracts(cs []Contract) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j-1].ID > cs[j].ID; j-- {
			cs[j-1], cs[j] = cs[j], cs[j-1]
		}
	}
}

// waitingWouldFit 供测试验证递补后不变量（加锁）。
func (b *Book) waitingWouldFit(mask uint32, qty int64) bool {
	b.st.mu.Lock()
	defer b.st.mu.Unlock()
	return b.st.wouldFitLocked(mask, qty)
}

// consistentSnapshot 在单次持锁内验证全部不变量（供并发测试）。
func (b *Book) consistentSnapshot() bool {
	b.st.mu.Lock()
	defer b.st.mu.Unlock()
	return b.st.consistentLocked()
}
