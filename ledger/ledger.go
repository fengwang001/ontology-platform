// Package ledger 在 budget 与 plan 之上实现隐私预算账本的状态机。
package ledger

import (
	"errors"
	"sort"
	"sync"

	"ontology/budget"
	"ontology/plan"
)

// 查询状态。
const (
	Reserved = iota
	Running
	Committed
	Cancelled
)

const (
	maxDatasets = 4
	maxNow      = 1_000_000_000_000
)

type query struct {
	id       string
	analyst  string
	datasets []string
	cost     int64
	actual   int64
	window   int64
	state    int
}

// Ledger 是隐私预算账本。骨架占位。
type Ledger struct {
	mu       sync.Mutex
	wn       int64
	clock    int64
	datasets map[string]*budget.Dataset
	analysts map[string]*budget.Analyst
	queries  map[string]*query

	// touched 为最近一次 Reserve/Commit/Cancel 读写的账目条数。
	touched int
	// nodes 为最近一次 Reserve 计划求值访问的节点数（恰为节点总数）。
	nodes int
}

// New 创建账本。Wn 必须在 [1, 10^9] 秒。
func New(wn int64) (*Ledger, error) {
	if wn < 1 || wn > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	return &Ledger{
		wn:       wn,
		datasets: map[string]*budget.Dataset{},
		analysts: map[string]*budget.Analyst{},
		queries:  map[string]*query{},
	}, nil
}

// AddDataset 登记数据集及其终身额度 Bd（1 到 10^12，永不恢复）。
func (l *Ledger) AddDataset(name string, bd int64) error {
	d, err := budget.NewDataset(name, bd)
	if err != nil {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.datasets[name]; exists {
		return ErrDuplicateQID
	}
	l.datasets[name] = d
	return nil
}

// AddAnalyst 登记分析师及其每窗口额度 Ba（1 到 10^12，跨数据集合计）。
func (l *Ledger) AddAnalyst(name string, ba int64) error {
	a, err := budget.NewAnalyst(name, ba)
	if err != nil {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.analysts[name]; exists {
		return ErrDuplicateQID
	}
	l.analysts[name] = a
	return nil
}

// Reserve 预留一次查询的预算。
func (l *Ledger) Reserve(qid, analyst string, datasets []string, p plan.Node, now int64) error {
	// 1) 参数非法：基础参数与计划求值（结构 + 开销上限 + Par 相交均在此暴露，
	// 其中 ErrNotDisjoint 被缓存，待存在性检查后再按拒绝次序报出）。
	if qid == "" || analyst == "" || now < 0 || now > maxNow {
		return ErrInvalidArgument
	}
	if len(datasets) < 1 || len(datasets) > maxDatasets {
		return ErrInvalidArgument
	}
	ev, err := plan.Eval(p)
	var disjointErr error
	if err != nil {
		if errors.Is(err, plan.ErrNotDisjoint) {
			disjointErr = ErrNotDisjoint
		} else {
			return ErrInvalidArgument
		}
	}
	cost := ev.Cost

	l.mu.Lock()
	defer l.mu.Unlock()
	l.touched = 0
	l.nodes = ev.Nodes

	// 2) 时钟回退
	if now < l.clock {
		return ErrClockRewind
	}
	// 3) qid 重复
	if _, ok := l.queries[qid]; ok {
		return ErrDuplicateQID
	}
	// 4) 数据集 / 分析师存在性（按数据集名字节序检查）
	names := append([]string(nil), datasets...)
	sort.Strings(names)
	if uniq(names) {
		return ErrInvalidArgument
	}
	ds := make([]*budget.Dataset, 0, len(names))
	for _, name := range names {
		d, ok := l.datasets[name]
		if !ok {
			return ErrUnknownDataset
		}
		ds = append(ds, d)
	}
	a, ok := l.analysts[analyst]
	if !ok {
		return ErrUnknownAnalyst
	}
	// 5) Par 分区相交
	if disjointErr != nil {
		return disjointErr
	}
	// 6) 数据集额度（已按名字节序，报第一个不足者）
	for _, d := range ds {
		l.touched++ // 每条数据集账算一条
		if !d.CanReserve(cost) {
			return ErrDatasetExhausted
		}
	}
	// 7) 分析师窗口额度
	w := l.window(now)
	l.touched++ // 分析师窗口账一条
	if !a.CanWindow(w, cost) {
		return ErrAnalystExhausted
	}

	// 全部够才生效
	for _, d := range ds {
		d.Reserve(cost)
	}
	a.AddWindow(w, cost)
	l.queries[qid] = &query{
		id:       qid,
		analyst:  analyst,
		datasets: names,
		cost:     cost,
		window:   w,
		state:    Reserved,
	}
	l.clock = now
	l.touched++ // 查询记录一条
	return nil
}

// Start 把 Reserved 查询变为 Running。
func (l *Ledger) Start(qid string, now int64) error {
	if qid == "" || now < 0 || now > maxNow {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touched = 0
	if now < l.clock {
		return ErrClockRewind
	}
	q, ok := l.queries[qid]
	if !ok {
		return ErrUnknownQuery
	}
	l.touched++ // 查询记录一条
	if q.state != Reserved {
		return ErrWrongState
	}
	q.state = Running
	l.clock = now
	return nil
}

// Commit 结算 Running 查询，actual 为 0 到 cost 的实际记账额。
func (l *Ledger) Commit(qid string, actual, now int64) error {
	if qid == "" || now < 0 || now > maxNow || actual < 0 {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touched = 0
	if now < l.clock {
		return ErrClockRewind
	}
	q, ok := l.queries[qid]
	if !ok {
		return ErrUnknownQuery
	}
	l.touched++ // 查询记录一条
	if q.state != Running {
		return ErrWrongState
	}
	if actual > q.cost {
		return ErrOverspend
	}
	for _, name := range q.datasets {
		d := l.datasets[name]
		l.touched++ // 每条数据集账一条
		d.Settle(q.cost, actual)
	}
	a := l.analysts[q.analyst]
	l.touched++ // 分析师（所属）窗口账一条
	a.SetWindow(q.window, q.cost, actual)
	q.actual = actual
	q.state = Committed
	l.clock = now
	return nil
}

// Cancel 撤销查询：Reserved 全额退还；Running 按全额计入已用、不退分析师占用。
func (l *Ledger) Cancel(qid string, now int64) error {
	if qid == "" || now < 0 || now > maxNow {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touched = 0
	if now < l.clock {
		return ErrClockRewind
	}
	q, ok := l.queries[qid]
	if !ok {
		return ErrUnknownQuery
	}
	l.touched++ // 查询记录一条
	switch q.state {
	case Reserved:
		for _, name := range q.datasets {
			d := l.datasets[name]
			l.touched++ // 每条数据集账一条
			d.Settle(q.cost, 0)
		}
		a := l.analysts[q.analyst]
		l.touched++ // 分析师（所属）窗口账一条
		a.SetWindow(q.window, q.cost, 0)
		q.state = Cancelled
		l.clock = now
		return nil
	case Running:
		for _, name := range q.datasets {
			d := l.datasets[name]
			l.touched++ // 每条数据集账一条
			d.Settle(q.cost, q.cost)
		}
		a := l.analysts[q.analyst]
		l.touched++ // 分析师（所属）窗口账一条（占用保持 cost，余额不变）
		_ = a.Window(q.window)
		q.state = Cancelled
		l.clock = now
		return nil
	default:
		return ErrWrongState
	}
}

// Remaining 返回数据集当前剩余额度 Bd-used-resv（只读）。
func (l *Ledger) Remaining(dataset string) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d, ok := l.datasets[dataset]
	if !ok {
		return 0, ErrUnknownDataset
	}
	return d.Remaining(), nil
}

// AnalystRemaining 返回 now 所属窗口的分析师剩余额度（只读）。
func (l *Ledger) AnalystRemaining(analyst string, now int64) (int64, error) {
	if now < 0 || now > maxNow {
		return 0, ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now < l.clock {
		return 0, ErrClockRewind
	}
	a, ok := l.analysts[analyst]
	if !ok {
		return 0, ErrUnknownAnalyst
	}
	return a.WindowRemaining(l.window(now)), nil
}

func (l *Ledger) window(now int64) int64 { return now / l.wn }

// uniq 报告已排序字符串切片中是否存在重复。
func uniq(sorted []string) bool {
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] == sorted[i] {
			return true
		}
	}
	return false
}
