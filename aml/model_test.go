package aml

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type naiveDeposit struct {
	txID      string
	accountID string
	amount    int64
	date      int64
	reversed  bool
	covered   bool
}

type naiveModel struct {
	cfg       Config
	now       int64
	accounts  map[string]bool
	parent    map[string]string
	deposits  map[string]*naiveDeposit
	reports   []Report
	nextGroup int64
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:      cfg,
		accounts: make(map[string]bool),
		parent:   make(map[string]string),
		deposits: make(map[string]*naiveDeposit),
	}
}

func (m *naiveModel) find(accountID string) string {
	root := accountID
	for m.parent[root] != root {
		root = m.parent[root]
	}
	for m.parent[accountID] != root {
		next := m.parent[accountID]
		m.parent[accountID] = root
		accountID = next
	}
	return root
}

func (m *naiveModel) groupAccounts(root string) []string {
	var accounts []string
	for accountID := range m.accounts {
		if m.find(accountID) == root {
			accounts = append(accounts, accountID)
		}
	}
	sort.Strings(accounts)
	return accounts
}

func (m *naiveModel) window(root string, now int64) []*naiveDeposit {
	var deposits []*naiveDeposit
	for _, dep := range m.deposits {
		if dep.reversed || dep.amount < m.cfg.L || dep.amount >= m.cfg.H {
			continue
		}
		if m.find(dep.accountID) != root {
			continue
		}
		if dep.date <= subtractDays(now, int(m.cfg.D)) || dep.date > now {
			continue
		}
		deposits = append(deposits, dep)
	}
	sort.Slice(deposits, func(i, j int) bool {
		if deposits[i].date != deposits[j].date {
			return deposits[i].date < deposits[j].date
		}
		return deposits[i].txID < deposits[j].txID
	})
	return deposits
}

func (m *naiveModel) emit(root string, deposits []*naiveDeposit, now int64, trigger OperationRef) *Report {
	uncovered := false
	total := int64(0)
	for _, dep := range deposits {
		if !dep.covered {
			uncovered = true
		}
		total += dep.amount
	}
	if int64(len(deposits)) < m.cfg.K || total < m.cfg.H || !uncovered {
		return nil
	}

	ids := make([]string, 0, len(deposits))
	for _, dep := range deposits {
		dep.covered = true
		ids = append(ids, dep.txID)
	}
	report := Report{
		Number:         int64(len(m.reports)) + 1,
		Kind:           StructuredReport,
		Trigger:        trigger,
		At:             now,
		GroupID:        int64(len(m.reports)) + 1,
		Accounts:       m.groupAccounts(root),
		TransactionIDs: ids,
		Total:          total,
	}
	m.reports = append(m.reports, report)
	return &m.reports[len(m.reports)-1]
}

func (m *naiveModel) open(now int64, accountID string) error {
	if now < 0 || accountID == "" {
		return ErrInvalidArgument
	}
	if now < m.now {
		return ErrClockRollback
	}
	if m.accounts[accountID] {
		return ErrAccountAlreadyExists
	}
	m.accounts[accountID] = true
	m.parent[accountID] = accountID
	m.now = now
	return nil
}

func (m *naiveModel) deposit(now int64, accountID, txID string, amount int64) (*Report, error) {
	if now < 0 || accountID == "" || txID == "" || amount <= 0 {
		return nil, ErrInvalidArgument
	}
	if now < m.now {
		return nil, ErrClockRollback
	}
	if !m.accounts[accountID] {
		return nil, ErrAccountNotFound
	}
	if _, exists := m.deposits[txID]; exists {
		return nil, ErrDuplicateTransaction
	}

	dep := &naiveDeposit{txID: txID, accountID: accountID, amount: amount, date: now}
	m.deposits[txID] = dep
	m.now = now

	if amount >= m.cfg.H {
		root := m.find(accountID)
		report := Report{
			Number:         int64(len(m.reports)) + 1,
			Kind:           LargeReport,
			Trigger:        OperationRef{Kind: DepositOperation, ID: txID},
			At:             now,
			GroupID:        int64(len(m.reports)) + 1,
			Accounts:       m.groupAccounts(root),
			TransactionIDs: []string{txID},
			Total:          amount,
		}
		m.reports = append(m.reports, report)
		return &m.reports[len(m.reports)-1], nil
	}
	if amount < m.cfg.L {
		return nil, nil
	}
	root := m.find(accountID)
	return m.emit(root, m.window(root, now), now, OperationRef{Kind: DepositOperation, ID: txID}), nil
}

func (m *naiveModel) link(now int64, a, b string) (*Report, error) {
	if now < 0 || a == "" || b == "" {
		return nil, ErrInvalidArgument
	}
	if now < m.now {
		return nil, ErrClockRollback
	}
	if !m.accounts[a] {
		return nil, ErrAccountNotFound
	}
	if !m.accounts[b] {
		return nil, ErrAccountNotFound
	}
	rootA := m.find(a)
	rootB := m.find(b)
	if rootA == rootB {
		return nil, ErrAlreadyLinked
	}

	firstID, secondID := a, b
	if secondID < firstID {
		firstID, secondID = secondID, firstID
	}
	m.parent[rootB] = rootA
	m.now = now
	root := m.find(a)
	return m.emit(root, m.window(root, now), now, OperationRef{Kind: LinkOperation, ID: firstID + "->" + secondID}), nil
}

func (m *naiveModel) reverse(now int64, txID string) error {
	if now < 0 || txID == "" {
		return ErrInvalidArgument
	}
	if now < m.now {
		return ErrClockRollback
	}
	dep, exists := m.deposits[txID]
	if !exists {
		return ErrTransactionNotFound
	}
	if dep.reversed {
		return ErrAlreadyReversed
	}
	dep.reversed = true
	m.now = now
	return nil
}

func (m *naiveModel) summary(accountID string, now int64) WindowSummary {
	root := m.find(accountID)
	deposits := m.window(root, now)
	return WindowSummary{Count: int64(len(deposits)), Total: reportTotal(deposits)}
}

func reportTotal(deposits []*naiveDeposit) int64 {
	var total int64
	for _, dep := range deposits {
		total += dep.amount
	}
	return total
}

func comparableReports(reports []Report) []Report {
	clones := make([]Report, len(reports))
	for i := range reports {
		clones[i] = reports[i]
		clones[i].GroupID = 0
		clones[i].Number = int64(i + 1)
		clones[i].Accounts = append([]string(nil), reports[i].Accounts...)
		clones[i].TransactionIDs = append([]string(nil), reports[i].TransactionIDs...)
	}
	return clones
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := Config{
				L: int64(1 + rng.Intn(20)),
				K: int64(2 + rng.Intn(3)),
				D: int64(1 + rng.Intn(5)),
			}
			cfg.H = cfg.L + int64(rng.Intn(30)+1)
			system, err := NewSystem(cfg)
			if err != nil {
				t.Fatalf("NewSystem(%+v) error = %v", cfg, err)
			}
			model := newNaiveModel(cfg)

			var accounts []string
			now := int64(0)
			txCounter := 0
			var trace strings.Builder
			fail := func(format string, args ...any) {
				t.Helper()
				t.Fatalf("%s\n%s", fmt.Sprintf(format, args...), trace.String())
			}

			ensureAccount := func() string {
				if len(accounts) == 0 || rng.Intn(10) == 0 {
					id := fmt.Sprintf("acct-%d", len(accounts))
					accounts = append(accounts, id)
					if err := system.OpenAccount(now, id); err != nil {
						t.Fatalf("system open: %v", err)
					}
					if err := model.open(now, id); err != nil {
						t.Fatalf("model open: %v", err)
					}
					fmt.Fprintf(&trace, "seed=%d open now=%d account=%s => accepted\n", seed, now, id)
				}
				return accounts[rng.Intn(len(accounts))]
			}

			for step := 0; step < 120; step++ {
				now += int64(rng.Intn(3))
				accountID := ensureAccount()
				op := rng.Intn(10)
				var log strings.Builder
				outcome := "accepted report=<nil>"
				fmt.Fprintf(&log, "seed=%d step=%d cfg=%+v now=%d op=%d", seed, step, cfg, now, op)

				switch {
				case op < 5:
					txCounter++
					txID := fmt.Sprintf("tx-%d", txCounter)
					var amount int64
					switch rng.Intn(8) {
					case 0:
						amount = cfg.L - 1
					case 1:
						amount = cfg.L
					case 2:
						amount = cfg.H - 1
					case 3:
						amount = cfg.H
					default:
						amount = cfg.L + int64(rng.Intn(int(cfg.H-cfg.L+3)))
					}
					if amount <= 0 {
						amount = cfg.L
					}
					fmt.Fprintf(&log, " deposit account=%s tx=%s amount=%d", accountID, txID, amount)
					systemReport, systemErr := system.Deposit(now, accountID, txID, amount)
					modelReport, modelErr := model.deposit(now, accountID, txID, amount)
					if !errors.Is(systemErr, modelErr) {
						fail("%s\nsystem error=%v model error=%v", log.String(), systemErr, modelErr)
					}
					if !reflect.DeepEqual(reportView(systemReport), reportView(modelReport)) {
						fail("%s\nsystem report=%+v\nmodel report=%+v", log.String(), reportView(systemReport), reportView(modelReport))
					}
					outcome = formatOutcome(systemErr, systemReport)
				case op < 8:
					other := ensureAccount()
					fmt.Fprintf(&log, " link %s %s", accountID, other)
					systemReport, systemErr := system.Link(now, accountID, other)
					modelReport, modelErr := model.link(now, accountID, other)
					if !errors.Is(systemErr, modelErr) {
						fail("%s\nsystem error=%v model error=%v", log.String(), systemErr, modelErr)
					}
					if !reflect.DeepEqual(reportView(systemReport), reportView(modelReport)) {
						fail("%s\nsystem report=%+v\nmodel report=%+v", log.String(), reportView(systemReport), reportView(modelReport))
					}
					outcome = formatOutcome(systemErr, systemReport)
				default:
					txID := fmt.Sprintf("tx-%d", rng.Intn(txCounter+2))
					fmt.Fprintf(&log, " reverse %s", txID)
					systemErr := system.Reverse(now, txID)
					modelErr := model.reverse(now, txID)
					if !errors.Is(systemErr, modelErr) {
						fail("%s\nsystem error=%v model error=%v", log.String(), systemErr, modelErr)
					}
					outcome = formatError(systemErr)
				}

				for _, queryAccount := range accounts {
					systemAccounts, err := system.GroupAccounts(now, queryAccount)
					if err != nil {
						fail("system groups: %v", err)
					}
					modelAccounts := model.groupAccounts(model.find(queryAccount))
					if !reflect.DeepEqual(systemAccounts, modelAccounts) {
						fail("%s query=%s system accounts=%v model accounts=%v", log.String(), queryAccount, systemAccounts, modelAccounts)
					}
					systemSummary, err := system.WindowSummary(now, queryAccount)
					if err != nil {
						fail("system summary: %v", err)
					}
					modelSummary := model.summary(queryAccount, now)
					if systemSummary != modelSummary {
						fail("%s query=%s system summary=%+v model summary=%+v", log.String(), queryAccount, systemSummary, modelSummary)
					}
					fmt.Fprintf(&log, " | query=%s count=%d total=%d", queryAccount, modelSummary.Count, modelSummary.Total)
				}
				systemReports, err := system.Reports(now)
				if err != nil {
					fail("system reports: %v", err)
				}
				if !reflect.DeepEqual(comparableReports(systemReports), comparableReports(model.reports)) {
					fail("%s\nsystem reports=%+v\nmodel reports=%+v", log.String(), comparableReports(systemReports), comparableReports(model.reports))
				}
				root := model.find(accountID)
				windowDeposits := model.window(root, now)
				fmt.Fprintf(&log, " | window=[")
				for i, dep := range windowDeposits {
					if i > 0 {
						fmt.Fprint(&log, ",")
					}
					fmt.Fprintf(&log, "%s:%d:covered=%t", dep.txID, dep.amount, dep.covered)
				}
				fmt.Fprintf(&log, "] reports=%d\n", len(systemReports))
				trace.WriteString(log.String())
				t.Logf("%s => %s | window=%s reports=%d", strings.TrimSpace(log.String()), outcome, formatWindow(model.window(model.find(accountID), now)), len(systemReports))
			}
		})
	}
}

func formatOutcome(err error, report *Report) string {
	if err != nil {
		return formatError(err)
	}
	if report == nil {
		return "accepted report=<nil>"
	}
	return fmt.Sprintf("accepted report=#%d kind=%s total=%d tx=%v", report.Number, report.Kind, report.Total, report.TransactionIDs)
}

func formatError(err error) string {
	if err == nil {
		return "accepted"
	}
	return "rejected error=" + err.Error()
}

func formatWindow(deposits []*naiveDeposit) string {
	var result strings.Builder
	result.WriteByte('[')
	for i, dep := range deposits {
		if i > 0 {
			result.WriteByte(',')
		}
		fmt.Fprintf(&result, "%s:%d:covered=%t", dep.txID, dep.amount, dep.covered)
	}
	result.WriteByte(']')
	return result.String()
}

func reportView(report *Report) Report {
	if report == nil {
		return Report{}
	}
	clone := *report
	clone.GroupID = 0
	return clone
}
