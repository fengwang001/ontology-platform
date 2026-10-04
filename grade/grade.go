// Package grade 登记退货收货分级并结算本次实付退款。
package grade

import (
	"ontology/refund"
	"ontology/rma"
)

// Grade 为收货分级。
type Grade int

const (
	// A：全新可售；B：开封可翻新；C：损坏拒退，原物退回客户。
	A Grade = 1
	// B 级按 β 系数退款。
	B Grade = 2
	// C 级原物退回客户：应退 0、不扣费、不占可退余量。
	C Grade = 3
)

// Result 是一次收货的退款结果。
type Result struct {
	Due  int64 // 本次应退 x = R新 − R旧（C 为 0）
	Fee  int64 // 本次手续费扣款 d
	Paid int64 // 本次实付 x − d
}

// Service 组合 rma.Store 与 refund.Calculator。
type Service struct {
	store *rma.Store
	calc  *refund.Calculator
}

// New 构造收货服务。
func New(store *rma.Store, calc *refund.Calculator) *Service {
	return &Service{store: store, calc: calc}
}

// Authorize 透传创建授权单，并同步开立手续费欠额账。
func (svc *Service) Authorize(id, order string, items []rma.Item, now int64) (int64, error) {
	return svc.store.AuthorizeHook(id, order, items, now, func(int64) { svc.calc.Open(id) })
}

// Receive 登记一次收货。
// 拒绝次序：参数非法 > 时钟回退 > 不存在（授权单、该授权单不含此行）
// > 已过期 > 超收。被拒绝时不改任何状态。
func (svc *Service) Receive(auth, order, line string, qty int64, g Grade, now int64) (Result, error) {
	if auth == "" || order == "" || line == "" || qty < 1 || g < A || g > C {
		return Result{}, rma.ErrInvalid
	}
	var res Result
	err := svc.store.Txn(now, func(t *rma.Txn) error {
		a, err := t.Auth(auth)
		if err != nil {
			return err
		}
		// 不存在先于已过期：订单不符或原始申请不含此行才算 NotFound；
		// 已收完的行仍“含”于授权单，后续由超收/到期判定。
		if a.OrderID() != order || !a.Contains(line) {
			return rma.ErrNotFound
		}
		// 只读校验“已过期”（恰等即失效），先不落地任何到期。
		if !a.Valid() || a.Exp() <= now {
			return rma.ErrExpired
		}
		if qty > a.Open(line) {
			return rma.ErrCapacity
		}
		// 全部校验通过：惰性落地到期（仅作废其他到期单的欠额；本单未到期）。
		for _, id := range t.ExpireDue() {
			svc.calc.Void(id)
		}
		if g == C {
			// C：消耗授权未收、不计合格数，应退 0、不扣手续费。
			t.ReceiveC(a, line, qty)
			return nil
		}
		d := t.Receive(a, line, qty, g == A)
		rOld := svc.calc.Cumulative(d.Paid, d.Shipped, d.OldA, d.OldB)
		rNew := svc.calc.Cumulative(d.Paid, d.Shipped, d.NewA, d.NewB)
		x := rNew - rOld
		fee, paid := svc.calc.Settle(auth, x)
		res = Result{Due: x, Fee: fee, Paid: paid}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}
