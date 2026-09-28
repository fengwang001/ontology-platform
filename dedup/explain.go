package dedup

import "strconv"

// Explain 返回该拒绝原因背后的判定依据（人类可读的规则说明）。
func (r RejectReason) Explain() string {
	switch r {
	case ReasonEmptyGroup:
		return "组名不能为空字符串；空组名无法归属多重性，拒绝整批"
	case ReasonEmptyValue:
		return "值不能为空字符串；空值不参与去重，拒绝整批"
	case ReasonInvalidSign:
		return "变更符号只允许 +1（插入）或 -1（撤回），其余符号一律拒绝整批"
	case ReasonWithdrawZero:
		return "撤回在已提交状态叠加批内此前各条后校验；当前净次数为零时撤回会使多重性为负，拒绝整批"
	case ReasonTooManyEntries:
		return "批内条目数超过构造时设定的上限，拒绝整批"
	default:
		return "未知拒绝原因：" + string(r)
	}
}

// String 返回条目的紧凑表示，如 `(orders,cust-7,+1)`，用于日志打印。
func (e Entry) String() string {
	return "(" + e.Group + "," + e.Value + "," + signString(e.Delta) + ")"
}

// String 返回组变化的紧凑表示，如 `orders 1->3 (Δ+2)`，用于日志打印。
func (c GroupChange) String() string {
	d := c.After - c.Before
	return c.Group + " " + strconv.Itoa(c.Before) + "->" + strconv.Itoa(c.After) +
		" (Δ" + signString(d) + ")"
}

// signString 把数值渲染为带符号字符串（0 渲染为 "0"）。
func signString(n int) string {
	if n > 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
