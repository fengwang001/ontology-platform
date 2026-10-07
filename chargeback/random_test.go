package chargeback_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/chargeback"
	"ontology/naive"
)

// system 抽象引擎与朴素模型的公共接口，供随机对照测试驱动。
type system interface {
	AddTransaction(now int, txn chargeback.Transaction) error
	OpenDispute(now int, caseID, txnID string, reason chargeback.Reason, amount int64) error
	Respond(now int, caseID string) error
	Accept(now int, caseID string) error
	PreArbitrate(now int, caseID string) error
	Rule(now int, caseID string, outcome chargeback.Outcome) error
	MerchantBalance(now int, merchantID string) (int64, error)
	IssuerBalance(now int) (int64, error)
	PendingHeld(now int) (int64, error)
	TotalFees(now int) (int64, error)
	DisputableAmount(now int, txnID string) (int64, error)
	CaseState(now int, caseID string) (chargeback.State, error)
	LedgerSummary(now int) (chargeback.LedgerSummary, error)
}

// op 是一条可重放的操作：desc 为输入描述，run 返回可比较的结果串。
type op struct {
	desc string
	run  func(s system) (string, error)
}

// generator 生成偏向合法边界的随机操作序列。
type generator struct {
	rnd       *rand.Rand
	now       int
	txns      []chargeback.Transaction
	cases     []string
	merchants []string
	caseSeq   int
	txnSeq    int
}

func newGenerator(seed int64) *generator {
	return &generator{
		rnd:       rand.New(rand.NewSource(seed)),
		merchants: []string{"mch-a", "mch-b"},
	}
}

func (g *generator) advanceNow() {
	switch r := g.rnd.Intn(100); {
	case r < 5: // 时钟回退，验证被拒绝
		g.now -= g.rnd.Intn(5) + 1
	case r < 15: // 大跳步，跨过各类截止日
		g.now += 10 + g.rnd.Intn(30)
	default:
		g.now += g.rnd.Intn(4)
	}
	if g.now < 0 {
		g.now = 0
	}
}

func (g *generator) randTxn() chargeback.Transaction {
	return g.txns[g.rnd.Intn(len(g.txns))]
}

func (g *generator) randCase() string {
	if len(g.cases) == 0 || g.rnd.Intn(20) == 0 {
		return "ghost-case"
	}
	return g.cases[g.rnd.Intn(len(g.cases))]
}

func (g *generator) randReason() chargeback.Reason {
	return chargeback.Reason(g.rnd.Intn(4))
}

// next 生成下一条随机操作。
func (g *generator) next() op {
	g.advanceNow()
	now := g.now
	switch roll := g.rnd.Intn(100); {
	case roll < 15 || len(g.txns) == 0: // 登记交易
		g.txnSeq++
		settle := now
		if g.rnd.Intn(4) != 0 {
			settle = now - g.rnd.Intn(6)
			if settle < 0 {
				settle = 0
			}
		}
		amount := int64(1 + g.rnd.Intn(400))
		// 小卡池 + 小金额池，提高重复扣款命中概率。
		card := fmt.Sprintf("card-%d", g.rnd.Intn(3))
		if g.rnd.Intn(3) == 0 {
			amount = 100
		}
		mch := g.merchants[g.rnd.Intn(len(g.merchants))]
		id := fmt.Sprintf("txn-%d", g.txnSeq)
		if len(g.txns) > 0 && g.rnd.Intn(15) == 0 {
			id = g.txns[0].ID // 重复登记：参数非法
		} else {
			g.txns = append(g.txns, chargeback.Transaction{
				ID: id, SettleDay: settle, Amount: amount, CardID: card, MerchantID: mch,
			})
		}
		txn := chargeback.Transaction{
			ID: id, SettleDay: settle, Amount: amount, CardID: card, MerchantID: mch,
		}
		return op{
			desc: fmt.Sprintf("AddTransaction(now=%d, %+v)", now, txn),
			run:  func(s system) (string, error) { return "", s.AddTransaction(now, txn) },
		}
	case roll < 45: // 提起拒付
		txn := g.randTxn()
		if g.rnd.Intn(20) == 0 {
			txn.ID = "ghost-txn"
		}
		g.caseSeq++
		caseID := fmt.Sprintf("case-%d", g.caseSeq)
		if len(g.cases) > 0 && g.rnd.Intn(15) == 0 {
			caseID = g.cases[0] // 重复案件号：参数非法
		} else {
			g.cases = append(g.cases, caseID)
		}
		reason := g.randReason()
		var amount int64
		switch g.rnd.Intn(6) {
		case 0:
			amount = 0 // 参数非法
		case 1:
			amount = txn.Amount // 恰好全额
		case 2:
			amount = txn.Amount + 1 // 超额
		default:
			amount = int64(1 + g.rnd.Intn(int(txn.Amount)+1))
		}
		return op{
			desc: fmt.Sprintf("OpenDispute(now=%d, case=%s, txn=%s, reason=%d, amount=%d)",
				now, caseID, txn.ID, reason, amount),
			run: func(s system) (string, error) {
				return "", s.OpenDispute(now, caseID, txn.ID, reason, amount)
			},
		}
	case roll < 58: // 应诉
		caseID := g.randCase()
		return op{
			desc: fmt.Sprintf("Respond(now=%d, case=%s)", now, caseID),
			run:  func(s system) (string, error) { return "", s.Respond(now, caseID) },
		}
	case roll < 68: // 接受应诉
		caseID := g.randCase()
		return op{
			desc: fmt.Sprintf("Accept(now=%d, case=%s)", now, caseID),
			run:  func(s system) (string, error) { return "", s.Accept(now, caseID) },
		}
	case roll < 78: // 预仲裁
		caseID := g.randCase()
		return op{
			desc: fmt.Sprintf("PreArbitrate(now=%d, case=%s)", now, caseID),
			run:  func(s system) (string, error) { return "", s.PreArbitrate(now, caseID) },
		}
	case roll < 88: // 裁决
		caseID := g.randCase()
		outcome := chargeback.Outcome(1 + g.rnd.Intn(2))
		if g.rnd.Intn(15) == 0 {
			outcome = chargeback.OutcomeNone // 参数非法
		}
		return op{
			desc: fmt.Sprintf("Rule(now=%d, case=%s, outcome=%d)", now, caseID, outcome),
			run:  func(s system) (string, error) { return "", s.Rule(now, caseID, outcome) },
		}
	default: // 查询
		return g.queryOp(now)
	}
}

// queryOp 生成一条随机查询。
func (g *generator) queryOp(now int) op {
	switch g.rnd.Intn(6) {
	case 0:
		mch := g.merchants[g.rnd.Intn(len(g.merchants))]
		return op{
			desc: fmt.Sprintf("MerchantBalance(now=%d, mch=%s)", now, mch),
			run: func(s system) (string, error) {
				v, err := s.MerchantBalance(now, mch)
				return fmt.Sprint(v), err
			},
		}
	case 1:
		return op{
			desc: fmt.Sprintf("IssuerBalance(now=%d)", now),
			run: func(s system) (string, error) {
				v, err := s.IssuerBalance(now)
				return fmt.Sprint(v), err
			},
		}
	case 2:
		return op{
			desc: fmt.Sprintf("PendingHeld(now=%d)", now),
			run: func(s system) (string, error) {
				v, err := s.PendingHeld(now)
				return fmt.Sprint(v), err
			},
		}
	case 3:
		return op{
			desc: fmt.Sprintf("TotalFees(now=%d)", now),
			run: func(s system) (string, error) {
				v, err := s.TotalFees(now)
				return fmt.Sprint(v), err
			},
		}
	case 4:
		txnID := "ghost-txn"
		if len(g.txns) > 0 && g.rnd.Intn(10) > 0 {
			txnID = g.randTxn().ID
		}
		return op{
			desc: fmt.Sprintf("DisputableAmount(now=%d, txn=%s)", now, txnID),
			run: func(s system) (string, error) {
				v, err := s.DisputableAmount(now, txnID)
				return fmt.Sprint(v), err
			},
		}
	default:
		caseID := g.randCase()
		return op{
			desc: fmt.Sprintf("CaseState(now=%d, case=%s)", now, caseID),
			run: func(s system) (string, error) {
				v, err := s.CaseState(now, caseID)
				return v.String(), err
			},
		}
	}
}

// TestRandomSequenceAgainstNaiveModel 用大量随机操作序列对照
// 引擎、重放引擎与独立朴素模型，验证三者结果完全一致。
func TestRandomSequenceAgainstNaiveModel(t *testing.T) {
	cfg := chargeback.Config{
		FraudWindowDays:       10,
		NotReceivedWindowDays: 12,
		DuplicateWindowDays:   15,
		DuplicateMatchDays:    6,
		ResponseWindowDays:    4,
		ReviewWindowDays:      3,
		ArbitrationFee:        7,
	}
	const seeds = 100
	const steps = 300
	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			g := newGenerator(seed)
			systems := []system{
				chargeback.New(cfg),
				chargeback.New(cfg), // 重放引擎：验证确定性
				naive.New(cfg),      // 独立朴素模型
			}
			names := []string{"engine", "replay", "naive"}
			maxNow := 0
			for step := 0; step < steps; step++ {
				o := g.next()
				if g.now > maxNow {
					maxNow = g.now
				}
				type result struct {
					payload string
					code    chargeback.ErrCode
					hasErr  bool
				}
				var want *result
				for i, s := range systems {
					payload, err := o.run(s)
					got := result{payload: payload, code: codeOf(err), hasErr: err != nil}
					t.Logf("seed=%d 步骤=%d 系统=%s 输入=[%s] 输出=[%s] 错误=[%v]",
						seed, step, names[i], o.desc, payload, err)
					if want == nil {
						want = &got
						continue
					}
					if got != *want {
						t.Fatalf("不一致: seed=%d 步骤=%d 输入=[%s] %s=%+v 而 %s=%+v",
							seed, step, o.desc, names[0], *want, names[i], got)
					}
				}
				// 每步后校验资金守恒（判定依据：守恒式恒为零），并比对
				// 三个系统的账目快照完全一致。查询同样推进时钟水位，
				// 因此必须对所有系统统一执行。
				var wantSum *chargeback.LedgerSummary
				for i, sys := range systems {
					sum, err := sys.LedgerSummary(maxNow)
					if err != nil {
						t.Fatalf("seed=%d 步骤=%d 系统=%s 账目快照失败: %v", seed, step, names[i], err)
					}
					if !sum.Balanced() {
						t.Fatalf("seed=%d 步骤=%d 系统=%s 资金不守恒: %+v（判定依据: 商户+发卡行+待决+仲裁费 应为零）",
							seed, step, names[i], sum)
					}
					if wantSum == nil {
						wantSum = &sum
					} else if sum != *wantSum {
						t.Fatalf("seed=%d 步骤=%d 账目快照不一致: %s=%+v 而 %s=%+v",
							seed, step, names[0], *wantSum, names[i], sum)
					}
				}
			}
			// 序列结束后做全量终态比对（所有案件状态与账目）。
			syncFinalState(t, g, systems, names, seed, maxNow)
		})
	}
}

// syncFinalState 在序列末尾比对三个系统的全部可观察状态。
func syncFinalState(t *testing.T, g *generator, systems []system, names []string, seed int64, now int) {
	t.Helper()
	queries := []op{}
	for _, txn := range g.txns {
		txnID := txn.ID
		queries = append(queries, op{
			desc: fmt.Sprintf("DisputableAmount(now=%d, txn=%s)", now, txnID),
			run: func(s system) (string, error) {
				v, err := s.DisputableAmount(now, txnID)
				return fmt.Sprint(v), err
			},
		})
	}
	for _, caseID := range g.cases {
		cid := caseID
		queries = append(queries, op{
			desc: fmt.Sprintf("CaseState(now=%d, case=%s)", now, cid),
			run: func(s system) (string, error) {
				v, err := s.CaseState(now, cid)
				return v.String(), err
			},
		})
	}
	for _, mch := range g.merchants {
		m := mch
		queries = append(queries, op{
			desc: fmt.Sprintf("MerchantBalance(now=%d, mch=%s)", now, m),
			run: func(s system) (string, error) {
				v, err := s.MerchantBalance(now, m)
				return fmt.Sprint(v), err
			},
		})
	}
	queries = append(queries,
		op{desc: "IssuerBalance", run: func(s system) (string, error) {
			v, err := s.IssuerBalance(now)
			return fmt.Sprint(v), err
		}},
		op{desc: "PendingHeld", run: func(s system) (string, error) {
			v, err := s.PendingHeld(now)
			return fmt.Sprint(v), err
		}},
		op{desc: "TotalFees", run: func(s system) (string, error) {
			v, err := s.TotalFees(now)
			return fmt.Sprint(v), err
		}},
	)
	for _, q := range queries {
		var wantPayload string
		var wantErr bool
		for i, s := range systems {
			payload, err := q.run(s)
			t.Logf("终态 seed=%d 系统=%s 输入=[%s] 输出=[%s] 错误=[%v]", seed, names[i], q.desc, payload, err)
			if i == 0 {
				wantPayload, wantErr = payload, err != nil
				continue
			}
			if payload != wantPayload || (err != nil) != wantErr {
				t.Fatalf("终态不一致: seed=%d 输入=[%s] %s=%s 而 %s=%s",
					seed, q.desc, names[0], wantPayload, names[i], payload)
			}
		}
	}
}
