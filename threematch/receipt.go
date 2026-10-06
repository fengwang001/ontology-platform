package threematch

import "math/big"

// Receive 对订单行收货 quantity 单位。
//
// 拒绝顺序：参数非法 > 时钟回退 > 对象不存在 > 超收。
// 收货不受供应商冻结影响。被拒绝时不改变任何状态与时钟。
func (s *Service) Receive(at int64, poID string, lineIndex, quantity int64) *Error {
	if poID == "" {
		return newError(KindInvalidParam, "订单ID不能为空")
	}
	if lineIndex < 0 {
		return newError(KindInvalidParam, "订单行下标不能为负: %d", lineIndex)
	}
	if quantity <= 0 {
		return newError(KindInvalidParam, "收货数量必须为正: %d", quantity)
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if err := s.st.checkClock(at); err != nil {
		return err
	}
	po, line, err := s.resolveLine(poID, lineIndex)
	if err != nil {
		// resolveLine 返回 KindLineNotFound；收货入口将其归入对象不存在。
		return newError(KindNotFound, "%s", err.Msg)
	}
	capQty := line.maxReceivableSafe(po.def.OverReceiptPermil)
	// 以减法比较，避免 received+quantity 溢出：
	// received + quantity > cap 等价于 quantity > cap - received。
	if quantity > capQty-line.received {
		return newError(KindOverReceipt,
			"订单 %s 行 %d 超收: 累计 %d+%d 超过上限 %d（订购 %d，容忍 %d）",
			poID, lineIndex, line.received, quantity, capQty,
			line.def.Quantity, line.overReceiptAllowanceSafe(po.def.OverReceiptPermil))
	}
	line.received += quantity
	s.st.advance(at)
	return nil
}

// ReverseReceipt 冲销订单行收货 quantity 单位。
//
// 拒绝顺序：参数非法 > 时钟回退 > 对象不存在 >
// 冲销为负（参数非法）> 已被发票占用（业务拒绝）。
func (s *Service) ReverseReceipt(at int64, poID string, lineIndex, quantity int64) *Error {
	if poID == "" {
		return newError(KindInvalidParam, "订单ID不能为空")
	}
	if lineIndex < 0 {
		return newError(KindInvalidParam, "订单行下标不能为负: %d", lineIndex)
	}
	if quantity <= 0 {
		return newError(KindInvalidParam, "冲销数量必须为正: %d", quantity)
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if err := s.st.checkClock(at); err != nil {
		return err
	}
	_, line, err := s.resolveLine(poID, lineIndex)
	if err != nil {
		return newError(KindNotFound, "%s", err.Msg)
	}
	if quantity > line.received {
		return newError(KindInvalidParam,
			"冲销数量 %d 超过累计收货量 %d", quantity, line.received)
	}
	net := line.received - quantity
	if net < line.invoicedQty {
		return newError(KindReceiptOccupied,
			"订单 %s 行 %d 冲销后累计收货 %d 低于已放行发票占用 %d",
			poID, lineIndex, net, line.invoicedQty)
	}
	line.received -= quantity
	s.st.advance(at)
	return nil
}

// ReceivedQuantity 返回某订单行当前累计收货量（只读，不推进时钟）。
func (s *Service) ReceivedQuantity(poID string, lineIndex int64) (int64, *Error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	_, line, err := s.resolveLine(poID, lineIndex)
	if err != nil {
		return 0, newError(KindNotFound, "%s", err.Msg)
	}
	return line.received, nil
}

// mulFloorDiv 返回 floor(a*b/1000) 的精确结果。a,b 非负且 b<=1000 时
// 通常不溢出；极端输入下退化为 big.Int 精确计算，结果超 int64 则饱和。
func mulFloorDiv(a, b int64) int64 {
	if p, ok := mulChecked(a, b); ok {
		return p / 1000
	}
	bi := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	bi.Div(bi, big.NewInt(1000))
	if bi.IsInt64() {
		return bi.Int64()
	}
	// 调用方只在容差场景使用（结果 <= a），真正到这里说明数值超 int64，
	// 返回饱和值交由后续比较处理。
	return mathMaxInt64
}

const mathMaxInt64 = 1<<63 - 1

// overReceiptAllowanceSafe 精确计算 floor(订购量*超收千分比/1000)。
func (l *poLineState) overReceiptAllowanceSafe(permil int64) int64 {
	return mulFloorDiv(l.def.Quantity, permil)
}

// maxReceivableSafe 精确计算订购量+容忍额，溢出时饱和到 MaxInt64。
func (l *poLineState) maxReceivableSafe(permil int64) int64 {
	allow := l.overReceiptAllowanceSafe(permil)
	if capQty, ok := addChecked(l.def.Quantity, allow); ok {
		return capQty
	}
	return mathMaxInt64
}
