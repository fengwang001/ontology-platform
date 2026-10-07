package actionguard

import "strconv"

// 本文件给出基于动作机制的具体示例：账户间转账与带手续费提现。
// 账户是对象，balance 为属性；金额以十进制字符串保存。
//
// 前置与后置的判断依据严格分离：
//   - 前置（sufficient_funds）只看执行前快照的单账户余额；
//   - 后置（total_conserved / non_negative_after）只看最终计划投影
//     出的多账户余额，且拿不到执行前快照，无法重评 sufficient_funds。

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatInt(int64(t), 10)
	}
	return ""
}

// NewTransferAction 账户间转账。
func NewTransferAction() *Action {
	return &Action{
		Type: "transfer",
		Pre: func(in map[string]any) ([]ConditionClause, error) {
			return []ConditionClause{
				{Name: "from_active", Lits: []Literal{
					{AtomSpec{"obj_exists", []Arg{VarArg("from")}}, true},
				}},
				{Name: "to_active", Lits: []Literal{
					{AtomSpec{"obj_exists", []Arg{VarArg("to")}}, true},
				}},
				{Name: "sufficient_funds", Lits: []Literal{
					{AtomSpec{"attr_gte", []Arg{VarArg("from"), ConstArg("balance"), VarArg("amount")}}, true},
				}},
			}, nil
		},
		Post: func(in map[string]any) ([]ConditionClause, error) {
			// 两个后置条件都只针对最终投影状态：
			// 转账后双方余额非负，且总额相对执行前保持守恒
			// （total 参数在 planner 中不可推导，因此由调用方把
			// 执行前总额作为输入给出；该原子只在投影快照上求值）。
			return []ConditionClause{
				{Name: "non_negative_from", Lits: []Literal{
					{AtomSpec{"attr_gte", []Arg{VarArg("from"), ConstArg("balance"), ConstArg("0")}}, true},
				}},
				{Name: "non_negative_to", Lits: []Literal{
					{AtomSpec{"attr_gte", []Arg{VarArg("to"), ConstArg("balance"), ConstArg("0")}}, true},
				}},
				{Name: "total_conserved", Lits: []Literal{
					{AtomSpec{"sum_attr_eq", []Arg{VarArg("allIdsSorted"), ConstArg("balance"), VarArg("total")}}, true},
				}},
			}, nil
		},
		Plan: func(in map[string]any, snap Snapshot) (*Plan, error) {
			from, to := str(in["from"]), str(in["to"])
			amount := num(str(in["amount"]))
			fromBal := num(attrOf(snap, from, "balance"))
			toBal := num(attrOf(snap, to, "balance"))
			return NewPlanBuilder().
				SetAttr(from, "balance", strconv.FormatInt(fromBal-amount, 10)).
				SetAttr(to, "balance", strconv.FormatInt(toBal+amount, 10)).
				Build(), nil
		},
	}
}

// NewWithdrawFeeAction 提现并收取固定手续费。
// 前置只要求余额 >= 提现额；但计划会额外扣除手续费，
// 当余额不足“提现额+手续费”时前置通过、后置失败，计划被整体放弃。
func NewWithdrawFeeAction(fee int64) *Action {
	feeStr := strconv.FormatInt(fee, 10)
	return &Action{
		Type: "withdraw_fee",
		Pre: func(in map[string]any) ([]ConditionClause, error) {
			return []ConditionClause{
				{Name: "account_active", Lits: []Literal{
					{AtomSpec{"obj_exists", []Arg{VarArg("from")}}, true},
				}},
				{Name: "sufficient_for_principal", Lits: []Literal{
					{AtomSpec{"attr_gte", []Arg{VarArg("from"), ConstArg("balance"), VarArg("amount")}}, true},
				}},
			}, nil
		},
		Post: func(in map[string]any) ([]ConditionClause, error) {
			return []ConditionClause{
				{Name: "non_negative_after_fee", Lits: []Literal{
					{AtomSpec{"attr_gte", []Arg{VarArg("from"), ConstArg("balance"), ConstArg("0")}}, true},
				}},
			}, nil
		},
		Plan: func(in map[string]any, snap Snapshot) (*Plan, error) {
			from := str(in["from"])
			amount := num(str(in["amount"]))
			bal := num(attrOf(snap, from, "balance"))
			return NewPlanBuilder().
				SetAttr(from, "balance", strconv.FormatInt(bal-amount-num(feeStr), 10)).
				Build(), nil
		},
	}
}

// NewContradictoryTransfer 声明期即自相矛盾的动作：
// 一个前置条件要求余额 >= amount，另一个前置条件又要求余额 < amount
// （同一原子模板、极性相反、可合一），任何输入下都不可能同时通过。
// Register 必须返回 *DefinitionError。
func NewContradictoryTransfer() *Action {
	return &Action{
		Type: "transfer_contradictory",
		Pre: func(in map[string]any) ([]ConditionClause, error) {
			return []ConditionClause{
				{Name: "sufficient_funds", Lits: []Literal{
					{AtomSpec{"attr_gte", []Arg{VarArg("from"), ConstArg("balance"), VarArg("amount")}}, true},
				}},
				{Name: "insufficient_funds", Lits: []Literal{
					{AtomSpec{"attr_gte", []Arg{VarArg("from"), ConstArg("balance"), VarArg("amount")}}, false},
				}},
			}, nil
		},
		Post: func(in map[string]any) ([]ConditionClause, error) { return nil, nil },
		Plan: func(in map[string]any, snap Snapshot) (*Plan, error) { return NewPlanBuilder().Build(), nil },
	}
}

func attrOf(snap Snapshot, id, attr string) string {
	v, _ := snap.Attr(id, attr)
	return v
}
