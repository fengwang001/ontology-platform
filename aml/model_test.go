package aml

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// 朴素模型：严格按规则字面实现，每次评估都扫描全部存款，
// 用于与引擎的增量实现对照。正确性优先，不考虑性能。

type mDeposit struct {
	account  string
	amount   int64
	date     int64
	reversed bool
}

type model struct {
	cfg      Config
	lastNow  int64
	hasNow   bool
	accounts map[string]bool
	memberOf map[string]int
	groups   map[int]map[string]bool
	nextGid  int
	deposits map[string]*mDeposit
	covered  map[string]bool
	reports  []Report
}

func newModel(cfg Config) *model {
	return &model{
		cfg:      cfg,
		accounts: map[string]bool{},
		memberOf: map[string]int{},
		groups:   map[int]map[string]bool{},
		deposits: map[string]*mDeposit{},
		covered:  map[string]bool{},
	}
}

func (m *model) checkClock(now int64) error {
	if m.hasNow && now < m.lastNow {
		return fmt.Errorf("%w: now=%d < 上次=%d", ErrClockRollback, now, m.lastNow)
	}
	return nil
}

func (m *model) emit(kind ReportKind, gid int, txnIDs []string, total int64, trigger string, now int64) *Report {
	sort.Strings(txnIDs)
	var accounts []string
	for acc := range m.groups[gid] {
		accounts = append(accounts, acc)
	}
	sort.Strings(accounts)
	m.reports = append(m.reports, Report{
		ID:       len(m.reports) + 1,
		Kind:     kind,
		Accounts: accounts,
		TxnIDs:   txnIDs,
		Total:    total,
		Trigger:  trigger,
		Date:     now,
	})
	return &m.reports[len(m.reports)-1]
}

// evaluate 扫描全部存款，计算该组在 now 的判定集合。
func (m *model) evaluate(gid int, now int64, trigger string) *Report {
	var set []string
	var total int64
	uncovered := false
	for txn, d := range m.deposits {
		if d.reversed || d.amount < m.cfg.Low || d.amount >= m.cfg.High {
			continue
		}
		if m.memberOf[d.account] != gid || now-d.date >= m.cfg.D {
			continue
		}
		set = append(set, txn)
		total += d.amount
		if !m.covered[txn] {
			uncovered = true
		}
	}
	if len(set) < m.cfg.K || total < m.cfg.High || !uncovered {
		return nil
	}
	for _, txn := range set {
		m.covered[txn] = true
	}
	return m.emit(KindStructuring, gid, set, total, trigger, now)
}

func (m *model) addAccount(acc string, now int64) error {
	if acc == "" {
		return fmt.Errorf("%w: 账户为空", ErrInvalidParam)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if m.accounts[acc] {
		return fmt.Errorf("%w: %s", ErrAccountExists, acc)
	}
	m.lastNow, m.hasNow = now, true
	m.accounts[acc] = true
	m.memberOf[acc] = m.nextGid
	m.groups[m.nextGid] = map[string]bool{acc: true}
	m.nextGid++
	return nil
}

func (m *model) deposit(txn, acc string, amount, now int64) (*Report, error) {
	if txn == "" || acc == "" || amount <= 0 {
		return nil, fmt.Errorf("%w", ErrInvalidParam)
	}
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	if !m.accounts[acc] {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, acc)
	}
	if _, ok := m.deposits[txn]; ok {
		return nil, fmt.Errorf("%w: %s", ErrDuplicateTxn, txn)
	}
	m.lastNow, m.hasNow = now, true
	m.deposits[txn] = &mDeposit{account: acc, amount: amount, date: now}
	gid := m.memberOf[acc]
	trigger := fmt.Sprintf("deposit(%s)", txn)
	if amount >= m.cfg.High {
		return m.emit(KindLarge, gid, []string{txn}, amount, trigger, now), nil
	}
	return m.evaluate(gid, now, trigger), nil
}

func (m *model) link(a, b string, now int64) (*Report, error) {
	if a == "" || b == "" {
		return nil, fmt.Errorf("%w: 账户为空", ErrInvalidParam)
	}
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	if !m.accounts[a] {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, a)
	}
	if !m.accounts[b] {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, b)
	}
	if m.memberOf[a] == m.memberOf[b] {
		return nil, fmt.Errorf("%w: %s 与 %s", ErrAlreadyLinked, a, b)
	}
	m.lastNow, m.hasNow = now, true
	gid := m.nextGid
	m.nextGid++
	m.groups[gid] = map[string]bool{}
	for _, old := range []int{m.memberOf[a], m.memberOf[b]} {
		for acc := range m.groups[old] {
			m.groups[gid][acc] = true
			m.memberOf[acc] = gid
		}
	}
	return m.evaluate(gid, now, fmt.Sprintf("link(%s,%s)", a, b)), nil
}

func (m *model) reverse(txn string, now int64) error {
	if txn == "" {
		return fmt.Errorf("%w: 交易号为空", ErrInvalidParam)
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	d, ok := m.deposits[txn]
	if !ok {
		return fmt.Errorf("%w: %s", ErrTxnNotFound, txn)
	}
	if d.reversed {
		return fmt.Errorf("%w: %s", ErrAlreadyReversed, txn)
	}
	m.lastNow, m.hasNow = now, true
	d.reversed = true
	return nil
}

func (m *model) currentSet(acc string, now int64) (int, int64, error) {
	if !m.accounts[acc] {
		return 0, 0, fmt.Errorf("%w: %s", ErrAccountNotFound, acc)
	}
	gid := m.memberOf[acc]
	count := 0
	var sum int64
	for _, d := range m.deposits {
		if d.reversed || d.amount < m.cfg.Low || d.amount >= m.cfg.High {
			continue
		}
		if m.memberOf[d.account] != gid || now-d.date >= m.cfg.D {
			continue
		}
		count++
		sum += d.amount
	}
	return count, sum, nil
}

func (m *model) groupAccounts(acc string) ([]string, error) {
	if !m.accounts[acc] {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, acc)
	}
	var out []string
	for a := range m.groups[m.memberOf[acc]] {
		out = append(out, a)
	}
	sort.Strings(out)
	return out, nil
}

// errKind 把错误归约到可比较的哨兵类型。
func errKind(err error) error {
	if err == nil {
		return nil
	}
	for _, s := range []error{
		ErrInvalidParam, ErrClockRollback, ErrAccountNotFound, ErrAccountExists,
		ErrDuplicateTxn, ErrTxnNotFound, ErrAlreadyReversed, ErrAlreadyLinked,
	} {
		if errors.Is(err, s) {
			return s
		}
	}
	return err
}

func sameReport(a, b *Report) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(*a, *b)
}

// 随机操作序列对照：引擎（增量实现）与朴素模型（全量扫描）必须
// 对每个操作返回相同的报告与错误，且任一时刻查询结果一致。
func TestRandomizedAgainstModel(t *testing.T) {
	cfg := testConfig()
	amounts := []int64{1, 50, 99, 100, 250, 333, 400, 999, 1000, 2000}
	for seed := int64(0); seed < 20; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			e, err := NewEngine(cfg)
			if err != nil {
				t.Fatal(err)
			}
			m := newModel(cfg)
			now := int64(0)
			acceptedNow := int64(0) // 已接受操作的最大 now（查询契约的下界）
			hasAccepted := false
			txnCounter := 0
			var txnPool []string

			pickAccount := func() string { return fmt.Sprintf("a%d", rng.Intn(10)) }
			accept := func(err error) {
				if err == nil && (!hasAccepted || now > acceptedNow) {
					acceptedNow = now
					hasAccepted = true
				}
			}
			advanceNow := func() {
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4, 5, 6, 7:
					now += int64(rng.Intn(3)) // 单调推进
				case 8:
					// 保持不变
				case 9:
					now -= int64(rng.Intn(4)) // 可能触发时钟回退
				}
			}
			checkQuery := func(step int) {
				acc := pickAccount()
				qnow := acceptedNow + int64(rng.Intn(10))
				ec, es, eerr := e.CurrentSet(acc, qnow)
				mc, ms, merr := m.currentSet(acc, qnow)
				if ec != mc || es != ms || errKind(eerr) != errKind(merr) {
					t.Fatalf("step %d CurrentSet(%s,%d): 引擎=(%d,%d,%v) 模型=(%d,%d,%v)",
						step, acc, qnow, ec, es, eerr, mc, ms, merr)
				}
				ea, eerr := e.GroupAccounts(acc)
				ma, merr := m.groupAccounts(acc)
				if errKind(eerr) != errKind(merr) || !reflect.DeepEqual(ea, ma) {
					t.Fatalf("step %d GroupAccounts(%s): 引擎=(%v,%v) 模型=(%v,%v)",
						step, acc, ea, eerr, ma, merr)
				}
			}

			for step := 0; step < 1500; step++ {
				advanceNow()
				switch op := rng.Intn(100); {
				case op < 15: // 注册账户（含重复注册）
					acc := pickAccount()
					eerr := e.AddAccount(acc, now)
					merr := m.addAccount(acc, now)
					accept(eerr)
					if errKind(eerr) != errKind(merr) {
						t.Fatalf("step %d AddAccount(%s,%d): 引擎=%v 模型=%v", step, acc, now, eerr, merr)
					}
					t.Logf("step %d AddAccount(acc=%s now=%d) -> err=%v", step, acc, now, errKind(eerr))
				case op < 55: // 存款（含重复交易号、非法金额、不存在账户）
					var txn string
					if len(txnPool) > 0 && rng.Intn(5) == 0 {
						txn = txnPool[rng.Intn(len(txnPool))]
					} else {
						txn = fmt.Sprintf("tx-%d", txnCounter)
						txnCounter++
					}
					acc := pickAccount()
					amount := amounts[rng.Intn(len(amounts))]
					er, eerr := e.Deposit(txn, acc, amount, now)
					mr, merr := m.deposit(txn, acc, amount, now)
					if errKind(eerr) != errKind(merr) || !sameReport(er, mr) {
						t.Fatalf("step %d Deposit(%s,%s,%d,%d): 引擎=(%+v,%v) 模型=(%+v,%v)",
							step, txn, acc, amount, now, er, eerr, mr, merr)
					}
					if eerr == nil {
						txnPool = append(txnPool, txn)
					}
					accept(eerr)
					t.Logf("step %d Deposit(txn=%s acc=%s amount=%d now=%d) -> report=%+v err=%v",
						step, txn, acc, amount, now, er, errKind(eerr))
				case op < 70: // 关联（含已关联、不存在账户）
					a, b := pickAccount(), pickAccount()
					er, eerr := e.Link(a, b, now)
					mr, merr := m.link(a, b, now)
					accept(eerr)
					if errKind(eerr) != errKind(merr) || !sameReport(er, mr) {
						t.Fatalf("step %d Link(%s,%s,%d): 引擎=(%+v,%v) 模型=(%+v,%v)",
							step, a, b, now, er, eerr, mr, merr)
					}
					t.Logf("step %d Link(%s,%s now=%d) -> report=%+v err=%v", step, a, b, now, er, errKind(eerr))
				case op < 85: // 冲正（含不存在、已冲正）
					txn := fmt.Sprintf("tx-%d", rng.Intn(txnCounter+3))
					eerr := e.Reverse(txn, now)
					merr := m.reverse(txn, now)
					accept(eerr)
					if errKind(eerr) != errKind(merr) {
						t.Fatalf("step %d Reverse(%s,%d): 引擎=%v 模型=%v", step, txn, now, eerr, merr)
					}
					t.Logf("step %d Reverse(txn=%s now=%d) -> err=%v", step, txn, now, errKind(eerr))
				default: // 查询对照
					checkQuery(step)
				}
			}
			// 终态全量对照：报告序列完全一致。
			er, mr := e.Reports(), m.reports
			if !reflect.DeepEqual(er, mr) {
				t.Fatalf("最终报告序列不一致:\n引擎=%v\n模型=%v", er, mr)
			}
			for _, acc := range []string{"a0", "a3", "a7", "ghost"} {
				ec, es, _ := e.CurrentSet(acc, acceptedNow)
				mc, ms, _ := m.currentSet(acc, acceptedNow)
				if ec != mc || es != ms {
					t.Fatalf("终态 CurrentSet(%s): 引擎=(%d,%d) 模型=(%d,%d)", acc, ec, es, mc, ms)
				}
			}
			t.Logf("seed=%d 完成: 报告 %d 份, 交易号 %d 个", seed, len(er), txnCounter)
		})
	}
}
