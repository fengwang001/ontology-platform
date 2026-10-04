// Package ledger 在 budget 账目之上实现查询预算的预留、启动、结算与撤销状态机。
package ledger

import (
	"errors"
	"sort"

	"ontology/budget"
	"ontology/plan"
	"sync"
)

const maxDatasetsPerQuery = 4

// 查询状态。
const (
	// Reserved：已预留未启动。
	statusReserved = iota
	// Running：已启动，待结算或撤销。
	statusRunning
	// Committed：已结算，终态。
	statusCommitted
	// Cancelled：已撤销（预留期或运行期撤销同入此终态）。
	statusCancelled
)

// 账本操作的哨兵错误，均可用 errors.Is 区分。
var (
	// ErrInvalidArg：参数非法（名字空、数据集数越界/重复、actual<0 等）。
	ErrInvalidArg = errors.New("ledger: invalid argument")
	// ErrInvalidPlan：计划结构越界或开销超过 10^12（对应 plan.ErrInvalidPlan）。
	ErrInvalidPlan = errors.New("ledger: invalid plan")
	// ErrClockRollback：now 越界或小于已接受的最大 now。
	ErrClockRollback = errors.New("ledger: clock rollback")
	// ErrDuplicateQID：qid 与已存在查询重复。
	ErrDuplicateQID = errors.New("ledger: duplicate query id")
	// ErrUnknownEntity：数据集或分析师不存在。
	ErrUnknownEntity = errors.New("ledger: unknown dataset or analyst")
	// ErrNotDisjoint：Par 子计划分区相交（即 plan.ErrNotDisjoint）。
	ErrNotDisjoint = plan.ErrNotDisjoint
	// ErrDatasetExhausted：某数据集终身额度不足。
	ErrDatasetExhausted = errors.New("ledger: dataset budget exhausted")
	// ErrAnalystExhausted：分析师当前窗口额度不足。
	ErrAnalystExhausted = errors.New("ledger: analyst window budget exhausted")
	// ErrUnknownQID：qid 不存在。
	ErrUnknownQID = errors.New("ledger: unknown query id")
	// ErrWrongState：查询状态不允许该操作（含对已结束查询的任何操作）。
	ErrWrongState = errors.New("ledger: query in wrong state")
	// ErrOverspend：Commit 的 actual 超出预留 cost。
	ErrOverspend = errors.New("ledger: commit actual exceeds reserved cost")
)

type query struct {
	analyst  string
	datasets []string // 已去重的数据集名（用于记账）
	cost     int64
	window   int64
	status   int
}

// Ledger 是预算账本。
type Ledger struct {
	mu      sync.Mutex
	b       *budget.Store
	queries map[string]*query
	// touched 为非导出计数器，记最近一次 Reserve/Commit/Cancel
	// 实际读写的账目条数（数据集账、分析师窗口账、查询记录各算一条）。
	touched int
}

// New 以窗口长度（秒）构造账本。
// windowSec 不在 1..10^9 时 panic（同 budget.New）。
func New(windowSec int64) *Ledger {
	return &Ledger{b: budget.New(windowSec), queries: map[string]*query{}}
}

// AddDataset 登记数据集及其终身额度 Bd（1..10^12，永不恢复）。
func (l *Ledger) AddDataset(name string, capB int64) error {
	return l.b.AddDataset(name, capB)
}

// AddAnalyst 登记分析师及其每窗口额度 Ba（跨数据集合计）。
func (l *Ledger) AddAnalyst(name string, capA int64) error {
	return l.b.AddAnalyst(name, capA)
}

func validName(name string) bool { return name != "" }

// normalizeDatasets 校验个数（1..4）并去重，保持稳定顺序。
func normalizeDatasets(in []string) ([]string, error) {
	if len(in) < 1 || len(in) > maxDatasetsPerQuery {
		return nil, ErrInvalidArg
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, name := range in {
		if !validName(name) {
			return nil, ErrInvalidArg
		}
		if _, dup := seen[name]; dup {
			return nil, ErrInvalidArg
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

// Reserve 校验并接受一次预算预留。拒绝次序见 DESIGN.md；被拒不改任何状态（含时钟）。
func (l *Ledger) Reserve(qid, analyst string, datasets []string, root plan.Node, now int64) (int64, error) {
	// 1. 参数非法（含计划结构越界；NotDisjoint 延后至实体存在性之后）。
	if !validName(qid) || !validName(analyst) || root == nil {
		return 0, ErrInvalidArg
	}
	ds, err := normalizeDatasets(datasets)
	if err != nil {
		return 0, err
	}
	planCost, _, planErr := plan.Eval(root)
	if errors.Is(planErr, plan.ErrInvalidPlan) {
		return 0, ErrInvalidPlan
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.touched = 0

	// 2. 时钟回退。
	if err := l.b.CheckClock(now); err != nil {
		return 0, ErrClockRollback
	}
	// 3. qid 重复——访问查询记录，计一条。
	if _, ok := l.queries[qid]; ok {
		l.touched++
		return 0, ErrDuplicateQID
	}
	l.touched++ // 即将写入的新查询记录

	// 4. 数据集或分析师不存在。
	for _, name := range ds {
		if !l.b.HasDataset(name) {
			return 0, ErrUnknownEntity
		}
	}
	if !l.b.HasAnalyst(analyst) {
		return 0, ErrUnknownEntity
	}
	// 5. ErrNotDisjoint（结构错已在锁前优先返回）。
	if planErr != nil {
		return 0, ErrNotDisjoint
	}

	// 6. 数据集按名字节序逐个检查，报第一个不足者。
	sorted := append([]string(nil), ds...)
	sort.Strings(sorted)
	for _, name := range sorted {
		l.touched++ // 读数据集账
		if !l.b.DatasetFits(name, planCost) {
			return 0, ErrDatasetExhausted
		}
	}
	// 7. 分析师 now 所属窗口额度（占用只计一次，不乘数据集个数）。
	window := l.b.Window(now)
	l.touched++ // 读/写分析师窗口账
	if !l.b.AnalystFits(analyst, window, planCost) {
		return 0, ErrAnalystExhausted
	}

	// 全部够才生效：一次性完成记账与状态写入。
	l.b.ReserveDatasets(ds, planCost)
	l.b.ReserveAnalyst(analyst, window, planCost)
	l.queries[qid] = &query{
		analyst:  analyst,
		datasets: ds,
		cost:     planCost,
		window:   window,
		status:   statusReserved,
	}
	l.b.AdvanceClock(now)
	return planCost, nil
}

// lookupLocked 取出查询；无论存在与否都计一条查询记录。
func (l *Ledger) lookupLocked(qid string) (*query, error) {
	q, ok := l.queries[qid]
	l.touched++
	if !ok {
		return nil, ErrUnknownQID
	}
	return q, nil
}

// Start 把 Reserved 查询变为 Running。
func (l *Ledger) Start(qid string, now int64) error {
	if !validName(qid) {
		return ErrInvalidArg
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touched = 0

	if err := l.b.CheckClock(now); err != nil {
		return ErrClockRollback
	}
	q, err := l.lookupLocked(qid)
	if err != nil {
		return err
	}
	if q.status != statusReserved {
		return ErrWrongState
	}
	q.status = statusRunning
	l.b.AdvanceClock(now)
	return nil
}

// Commit 结算 Running 查询：actual ∈ [0,cost]，resv 转 used，窗口占用 cost→actual。
func (l *Ledger) Commit(qid string, actual, now int64) error {
	if !validName(qid) || actual < 0 {
		return ErrInvalidArg
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touched = 0

	if err := l.b.CheckClock(now); err != nil {
		return ErrClockRollback
	}
	q, err := l.lookupLocked(qid)
	if err != nil {
		return err
	}
	if q.status != statusRunning {
		return ErrWrongState
}
	if actual > q.cost {
		return ErrOverspend
	}

	l.b.CommitRunning(q.datasets, q.analyst, q.window, q.cost, actual)
	l.touched += len(q.datasets) + 1 // 数据集账 + 分析师窗口账
	q.status = statusCommitted
	l.b.AdvanceClock(now)
	return nil
}

// Cancel 撤销查询：Reserved 全额退还；Running 不退、按全额记已用。
func (l *Ledger) Cancel(qid string, now int64) error {
	if !validName(qid) {
		return ErrInvalidArg
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touched = 0

	if err := l.b.CheckClock(now); err != nil {
		return ErrClockRollback
	}
	q, err := l.lookupLocked(qid)
	if err != nil {
		return err
	}
	switch q.status {
	case statusReserved:
		l.b.CancelReserve(q.datasets, q.analyst, q.window, q.cost)
		l.touched += len(q.datasets) + 1 // 数据集账 + 分析师窗口账
		q.status = statusCancelled
	case statusRunning:
		l.b.FinalizeRunningCancel(q.datasets, q.cost)
		l.touched += len(q.datasets) // 分析师占用保持 cost，不写窗口账
		q.status = statusCancelled
	default:
		return ErrWrongState // Committed/Cancelled 为已结束
	}
	l.b.AdvanceClock(now)
	return nil
}

// Remaining 返回数据集当前余额 Bd-used-resv；now 小于时钟报 ErrClockRollback。
func (l *Ledger) Remaining(dataset string, now int64) (int64, error) {
	if !validName(dataset) {
		return 0, ErrInvalidArg
	}
	if err := l.b.CheckClock(now); err != nil {
		return 0, ErrClockRollback
	}
	rem, err := l.b.DatasetRemaining(dataset)
	if err != nil {
		return 0, ErrUnknownEntity
	}
	return rem, nil
}

// AnalystRemaining 返回 now 所属窗口的 Ba 减占用（只读）；
// now 小于时钟报 ErrClockRollback。
func (l *Ledger) AnalystRemaining(analyst string, now int64) (int64, error) {
	if !validName(analyst) {
		return 0, ErrInvalidArg
	}
	if err := l.b.CheckClock(now); err != nil {
		return 0, ErrClockRollback
	}
	window := l.b.Window(now)
	rem, err := l.b.AnalystRemainingAtWindow(analyst, window)
	if err != nil {
		return 0, ErrUnknownEntity
	}
	return rem, nil
}
