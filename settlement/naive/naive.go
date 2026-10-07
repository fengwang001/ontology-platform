// Package naive 是结算规则的独立朴素实现：不复用 settlement 包的任何
// 结算逻辑，用全量切片扫描直译规则，专供差分测试充当参照模型。
// 它刻意保持 O(历史总量) 的单日开销，只追求显而易见的正确性。
package naive

import (
	"math"

	"ontology/settlement"
)

type tx struct {
	id      string
	day     int64
	amount  int64
	settled bool
}

type batch struct {
	retentionDay int64
	releaseDay   int64
	balance      int64
	released     bool
}

type merchant struct {
	cfg              settlement.Config
	lastSettled      int64
	carry            int64
	cumulativePayout int64
	txs              []tx
	batches          []batch
}

// Model 朴素结算模型，接口与 settlement.Engine 对齐。
type Model struct {
	cal       settlement.Calendar
	lastNow   int64
	merchants map[string]*merchant
}

// New 创建朴素模型。
func New(cal settlement.Calendar) *Model {
	return &Model{cal: cal, lastNow: math.MinInt64, merchants: make(map[string]*merchant)}
}

func codeOf(err error) settlement.Code {
	if err == nil {
		return -1
	}
	return err.(*settlement.Error).Code
}

// AddMerchant 与 Engine.AddMerchant 语义一致。
func (m *Model) AddMerchant(now int64, id string, cfg settlement.Config) error {
	if id == "" || cfg.DelayDays < 1 || cfg.HorizonDays < 1 || cfg.ReserveBps < 0 || cfg.ReserveBps > 10000 {
		return &settlement.Error{Code: settlement.CodeInvalidParam, Msg: "invalid param"}
	}
	if now < m.lastNow {
		return &settlement.Error{Code: settlement.CodeClockRollback, Msg: "clock rollback"}
	}
	if _, ok := m.merchants[id]; ok {
		return &settlement.Error{Code: settlement.CodeMerchantExists, Msg: "merchant exists"}
	}
	m.merchants[id] = &merchant{cfg: cfg, lastSettled: math.MinInt64}
	m.lastNow = now
	return nil
}

// PostTransaction 与 Engine.PostTransaction 语义一致。
func (m *Model) PostTransaction(now int64, merchantID, txID string, day int64, amount int64) error {
	if merchantID == "" || txID == "" {
		return &settlement.Error{Code: settlement.CodeInvalidParam, Msg: "invalid param"}
	}
	if now < m.lastNow {
		return &settlement.Error{Code: settlement.CodeClockRollback, Msg: "clock rollback"}
	}
	mc, ok := m.merchants[merchantID]
	if !ok {
		return &settlement.Error{Code: settlement.CodeMerchantNotFound, Msg: "merchant not found"}
	}
	for _, t := range mc.txs {
		if t.id == txID {
			return &settlement.Error{Code: settlement.CodeDuplicateTxID, Msg: "duplicate tx id"}
		}
	}
	if day > now {
		return &settlement.Error{Code: settlement.CodeInvalidDate, Msg: "tx day after now"}
	}
	if day < mc.lastSettled {
		return &settlement.Error{Code: settlement.CodeAlreadyClosed, Msg: "day already closed"}
	}
	mc.txs = append(mc.txs, tx{id: txID, day: day, amount: amount})
	m.lastNow = now
	return nil
}

// Settle 与 Engine.Settle 语义一致。
func (m *Model) Settle(now int64, merchantID string, d int64) ([]settlement.PayoutRecord, error) {
	if merchantID == "" {
		return nil, &settlement.Error{Code: settlement.CodeInvalidParam, Msg: "invalid param"}
	}
	if now < m.lastNow {
		return nil, &settlement.Error{Code: settlement.CodeClockRollback, Msg: "clock rollback"}
	}
	mc, ok := m.merchants[merchantID]
	if !ok {
		return nil, &settlement.Error{Code: settlement.CodeMerchantNotFound, Msg: "merchant not found"}
	}
	if !m.cal.IsBusinessDay(d) {
		return nil, &settlement.Error{Code: settlement.CodeNonBusinessDay, Msg: "not a business day"}
	}
	if d <= mc.lastSettled {
		return nil, &settlement.Error{Code: settlement.CodeDuplicateSettlement, Msg: "duplicate settlement"}
	}
	var records []settlement.PayoutRecord
	for _, t := range m.cal.DaysBetween(mc.lastSettled, d) {
		records = append(records, mc.settleDay(m.cal, merchantID, t))
	}
	m.lastNow = now
	return records, nil
}

func (mc *merchant) settleDay(cal settlement.Calendar, merchantID string, t int64) settlement.PayoutRecord {
	// 全量扫描：挑出发生日不晚于 cutoff 的未结算流水。
	var newlySettled int64
	if cutoff, ok := cal.Retreat(t, mc.cfg.DelayDays); ok {
		for i := range mc.txs {
			if !mc.txs[i].settled && mc.txs[i].day <= cutoff {
				mc.txs[i].settled = true
				newlySettled += mc.txs[i].amount
			}
		}
	}
	net := mc.carry + newlySettled

	// 全量扫描：释放所有到期批次。
	var release int64
	for i := range mc.batches {
		b := &mc.batches[i]
		if !b.released && b.balance > 0 && b.releaseDay <= t {
			release += b.balance
			b.released = true
		}
	}

	var newReserve, drawn, payout int64
	if net >= 0 {
		newReserve = ceilDiv(net*int64(mc.cfg.ReserveBps), 10000)
		if newReserve > 0 {
			rd, ok := cal.Advance(t, mc.cfg.HorizonDays)
			if !ok {
				rd = math.MaxInt64
			}
			mc.batches = append(mc.batches, batch{retentionDay: t, releaseDay: rd, balance: newReserve})
		}
		payout = net - newReserve + release
		mc.carry = 0
	} else {
		balance := net + release
		if balance < 0 {
			need := -balance
			// 按留存日由早到晚（切片即时间序）动用未到期批次。
			for i := range mc.batches {
				if need == 0 {
					break
				}
				b := &mc.batches[i]
				if b.released || b.balance == 0 || b.releaseDay <= t {
					continue
				}
				use := min(b.balance, need)
				b.balance -= use
				drawn += use
				need -= use
			}
			balance += drawn
		}
		if balance < 0 {
			mc.carry = balance
			payout = 0
		} else {
			mc.carry = 0
			payout = balance
		}
	}
	mc.cumulativePayout += payout
	mc.lastSettled = t
	return settlement.PayoutRecord{
		MerchantID: merchantID,
		Day:        t,
		Net:        net,
		Release:    release,
		NewReserve: newReserve,
		Drawn:      drawn,
		Payout:     payout,
		CarryAfter: mc.carry,
	}
}

func ceilDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 {
		q++
	}
	return q
}

// Snapshot 返回与 Engine.Snapshot 可比较的状态快照。
func (m *Model) Snapshot(merchantID string) (settlement.Snapshot, error) {
	mc, ok := m.merchants[merchantID]
	if !ok {
		return settlement.Snapshot{}, &settlement.Error{Code: settlement.CodeMerchantNotFound, Msg: "merchant not found"}
	}
	snap := settlement.Snapshot{
		MerchantID:       merchantID,
		LastSettledDay:   mc.lastSettled,
		CumulativePayout: mc.cumulativePayout,
		Carry:            mc.carry,
	}
	var settledSum int64
	pending := 0
	for _, t := range mc.txs {
		if t.settled {
			settledSum += t.amount
		} else {
			pending++
		}
	}
	snap.SettledTxSum = settledSum
	snap.PendingTxCount = pending
	for _, b := range mc.batches {
		if !b.released && b.balance > 0 {
			snap.ReserveBalance += b.balance
			snap.Batches = append(snap.Batches, settlement.ReserveBatch{
				RetentionDay: b.retentionDay,
				ReleaseDay:   b.releaseDay,
				Balance:      b.balance,
			})
		}
	}
	return snap, nil
}

// CodeOf 提取错误码，供测试比较两个实现的错误是否一致。
func CodeOf(err error) settlement.Code { return codeOf(err) }
