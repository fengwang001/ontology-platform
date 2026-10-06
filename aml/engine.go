package aml

import (
	"fmt"
	"sort"
	"sync"
)

// Engine 为反洗钱检测引擎。所有方法可并发调用，
// 结果等价于某个串行顺序；相同操作序列重放得到完全相同的报告序列。
type Engine struct {
	mu      sync.RWMutex
	cfg     Config
	lastNow int64 // 上一次被接受操作的 now
	hasNow  bool

	parent map[string]string // 并查集：账户 -> 父账户（根指向自身）
	size   map[string]int    // 并查集：根 -> 组成员数（按大小合并）
	groups map[string]*group // 并查集：根账户 -> 组数据

	deposits map[string]*deposit // 交易号 -> 存款（含已冲正，交易号一经使用即占用）
	reports  []Report
	nextID   int

	// visits 统计窗口条目被访问的次数，用于以可验证方式证明
	// 评估与关联的开销不随全部账户数、全部历史存款数、
	// 以及较大一组窗口之外的历史存款数增长。
	visits int64
}

// NewEngine 创建引擎；配置不合法时返回 ErrInvalidParam。
func NewEngine(cfg Config) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Engine{
		cfg:      cfg,
		parent:   map[string]string{},
		size:     map[string]int{},
		groups:   map[string]*group{},
		deposits: map[string]*deposit{},
		nextID:   1,
	}, nil
}

// AddAccount 注册账户，初始自成一组。携带 now，须不小于时钟。
func (e *Engine) AddAccount(account string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.addAccount(account, now)
}

// Deposit 存入一笔现金。存款日期即 now。
// 若单笔不小于 H 立即返回大额报告；否则若触发结构化判定则返回结构化报告；
// 一次被接受的操作最多产生一份报告，未产生时返回 nil。
func (e *Engine) Deposit(txnID, account string, amount int64, now int64) (*Report, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.deposit(txnID, account, amount, now)
}

// Link 关联两个账户，合并其所在客户组；已在同一组则报 ErrAlreadyLinked。
// 关联不可撤销。合并后若集合成立且仍有未覆盖存款，返回结构化报告。
func (e *Engine) Link(a, b string, now int64) (*Report, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.link(a, b, now)
}

// Reverse 冲正一笔存款，使其不再计入任何判定集合。
// 已发出的报告不撤回，已覆盖标记不恢复。冲正本身不触发评估。
func (e *Engine) Reverse(txnID string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reverse(txnID, now)
}

// GroupAccounts 返回账户当前所属客户组的全部账户（排序）。
// 查询不修改任何状态。
func (e *Engine) GroupAccounts(account string) ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.groupAccounts(account)
}

// CurrentSet 返回该账户所属客户组按给定 now 计算的判定集合的笔数与合计。
// 查询不修改任何状态，也不受查询次数影响。
// 前提：now 不小于已接受操作的最大 now（即“当前 now”）；
// 引擎按增量方式维护窗口，不保留回答历史时刻所需的数据。
func (e *Engine) CurrentSet(account string, now int64) (count int, sum int64, err error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentSet(account, now)
}

// Reports 返回已发出的全部报告（按全局编号次序）。
func (e *Engine) Reports() []Report {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.allReports()
}

// Visits 返回窗口条目累计被访问次数，用于复杂度验证。
func (e *Engine) Visits() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.visits
}

// checkClock 校验时钟；被拒绝的操作不改变任何状态、报告与时钟。
func (e *Engine) checkClock(now int64) error {
	if e.hasNow && now < e.lastNow {
		return fmt.Errorf("%w: now=%d < 上次=%d", ErrClockRollback, now, e.lastNow)
	}
	return nil
}

// accept 在接受操作时推进时钟。
func (e *Engine) accept(now int64) {
	e.lastNow = now
	e.hasNow = true
}

// emit 生成一份报告并追加到全局报告序列。
func (e *Engine) emit(kind ReportKind, g *group, txnIDs []string, total int64, trigger string, now int64) *Report {
	sort.Strings(txnIDs)
	accounts := make([]string, 0, len(g.members))
	for acc := range g.members {
		accounts = append(accounts, acc)
	}
	sort.Strings(accounts)
	e.reports = append(e.reports, Report{
		ID:       e.nextID,
		Kind:     kind,
		Accounts: accounts,
		TxnIDs:   txnIDs,
		Total:    total,
		Trigger:  trigger,
		Date:     now,
	})
	e.nextID++
	return &e.reports[len(e.reports)-1]
}

func (e *Engine) addAccount(account string, now int64) error {
	if account == "" {
		return fmt.Errorf("%w: 账户为空", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if _, ok := e.parent[account]; ok {
		return fmt.Errorf("%w: %s", ErrAccountExists, account)
	}
	e.accept(now)
	e.parent[account] = account
	e.size[account] = 1
	e.groups[account] = newGroup(account)
	return nil
}

func (e *Engine) deposit(txnID, account string, amount int64, now int64) (*Report, error) {
	if txnID == "" || account == "" || amount <= 0 {
		return nil, fmt.Errorf("%w: 交易号/账户为空或金额非正 (txn=%q acc=%q amount=%d)",
			ErrInvalidParam, txnID, account, amount)
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	if _, ok := e.parent[account]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, account)
	}
	if _, ok := e.deposits[txnID]; ok {
		return nil, fmt.Errorf("%w: %s", ErrDuplicateTxn, txnID)
	}
	e.accept(now)

	d := &deposit{txnID: txnID, account: account, amount: amount, date: now}
	e.deposits[txnID] = d
	root := e.find(account)
	trigger := fmt.Sprintf("deposit(%s)", txnID)

	if amount >= e.cfg.High {
		// 大额：立即报告，不进入任何结构化判定集合。
		return e.emit(KindLarge, e.groups[root], []string{txnID}, amount, trigger, now), nil
	}
	if amount >= e.cfg.Low {
		// 小额：进入窗口并评估。
		g := e.groups[root]
		d.inWindow = true
		g.window = append(g.window, d) // date=now 不早于窗口内任何日期
		g.count++
		g.sum += amount
		g.uncovered++
		return e.evaluate(root, now, trigger), nil
	}
	// 低于下限：只记录，不参与判定；仍按规则评估一次（结果必为空）。
	e.evaluate(root, now, trigger)
	return nil, nil
}

func (e *Engine) link(a, b string, now int64) (*Report, error) {
	if a == "" || b == "" {
		return nil, fmt.Errorf("%w: 账户为空", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	if _, ok := e.parent[a]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, a)
	}
	if _, ok := e.parent[b]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, b)
	}
	ra, rb := e.find(a), e.find(b)
	if ra == rb {
		return nil, fmt.Errorf("%w: %s 与 %s", ErrAlreadyLinked, a, b)
	}
	e.accept(now)
	root := e.merge(ra, rb, now)
	return e.evaluate(root, now, fmt.Sprintf("link(%s,%s)", a, b)), nil
}

func (e *Engine) reverse(txnID string, now int64) error {
	if txnID == "" {
		return fmt.Errorf("%w: 交易号为空", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	d, ok := e.deposits[txnID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrTxnNotFound, txnID)
	}
	if d.reversed {
		return fmt.Errorf("%w: %s", ErrAlreadyReversed, txnID)
	}
	e.accept(now)
	d.reversed = true
	if d.inWindow {
		d.inWindow = false
		g := e.groups[e.find(d.account)]
		g.count--
		g.sum -= d.amount
		if !d.covered {
			g.uncovered--
		}
	}
	return nil
}

func (e *Engine) groupAccounts(account string) ([]string, error) {
	if _, ok := e.parent[account]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, account)
	}
	g := e.groups[e.findRoot(account)]
	accounts := make([]string, 0, len(g.members))
	for acc := range g.members {
		accounts = append(accounts, acc)
	}
	sort.Strings(accounts)
	return accounts, nil
}

func (e *Engine) currentSet(account string, now int64) (int, int64, error) {
	if _, ok := e.parent[account]; !ok {
		return 0, 0, fmt.Errorf("%w: %s", ErrAccountNotFound, account)
	}
	g := e.groups[e.findRoot(account)]
	count := 0
	var sum int64
	for _, d := range g.window {
		if d.inWindow && !e.expired(d, now) {
			count++
			sum += d.amount
		}
	}
	return count, sum, nil
}

func (e *Engine) allReports() []Report {
	out := make([]Report, len(e.reports))
	copy(out, e.reports)
	return out
}
