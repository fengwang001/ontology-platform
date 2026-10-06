package threematch

import "math/big"

// SubmitInvoice 提交发票并立即判定。
//
// 拒绝次序（严格）：
//  1. 参数非法（发票号/供应商为空、无行、数量单价非正、下标为负、
//     行金额或合计溢出）
//  2. 时钟回退
//  3. 发票号重复（已放行与被保留均算）
//  4. 供应商冻结（冻结期间不产生被保留记录）
//  5. 供应商对象不存在
//  6. 逐行结构性校验：每行所属订单存在且属于该供应商，否则直接拒绝，
//     不保留（订单/行不存在为 KindLineNotFound，供应商不一致为
//     KindSupplierMismatch，均可与业务原因区分）
//  7. 逐行业务判定：价格不符 > 超开；
//     任一失败则整票保留，不占用任何额度，返回下标最小的失败行及原因。
func (s *Service) SubmitInvoice(at int64, number, supplierID string, lines []InvoiceLine) (SubmitResult, *Error) {
	if err := validateInvoiceArgs(number, supplierID, lines); err != nil {
		return SubmitResult{}, err
	}
	// 防御性拷贝，避免调用方在判定后修改切片。
	linesCopy := make([]InvoiceLine, len(lines))
	copy(linesCopy, lines)

	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if err := s.st.checkClock(at); err != nil {
		return SubmitResult{}, err
	}
	if _, ok := s.st.invoices[number]; ok {
		return SubmitResult{}, newError(KindDuplicate, "发票号重复: %s", number)
	}
	sup, ok := s.st.suppliers[supplierID]
	if !ok {
		return SubmitResult{}, newError(KindNotFound, "供应商不存在: %s", supplierID)
	}
	if sup.frozen {
		return SubmitResult{}, newError(KindSupplierFrozen, "供应商 %s 已冻结", supplierID)
	}
	if err := s.validateInvoiceLines(supplierID, linesCopy); err != nil {
		return SubmitResult{}, err
	}

	inv := &invoiceState{
		number:     number,
		supplierID: supplierID,
		lines:      linesCopy,
		failLine:   -1,
	}
	s.st.invoices[number] = inv
	res := s.evaluate(at, inv)
	// evaluate 只在全票放行时推进时钟；保留不占用额度，但“提交”本身
	// 是一次被接受的操作（产生了被保留记录），时钟必须推进。
	s.st.advance(at)
	return res, nil
}

// RejudgeInvoice 对被保留发票重新判定。放行按本次时刻记放行时刻；
// 仍失败则保持保留且不改变业务状态（时钟随本次被接受操作推进）。
//
// 拒绝次序：参数非法 > 时钟回退 > 对象不存在 > 供应商冻结（不产生额外
// 记录，原有保留记录原样保留）> 状态不符（已放行/已付款不能重判）。
func (s *Service) RejudgeInvoice(at int64, number string) (SubmitResult, *Error) {
	if number == "" {
		return SubmitResult{}, newError(KindInvalidParam, "发票号不能为空")
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if err := s.st.checkClock(at); err != nil {
		return SubmitResult{}, err
	}
	inv, ok := s.st.invoices[number]
	if !ok {
		return SubmitResult{}, newError(KindNotFound, "发票不存在: %s", number)
	}
	sup := s.st.suppliers[inv.supplierID]
	if sup.frozen {
		return SubmitResult{}, newError(KindSupplierFrozen, "供应商 %s 已冻结", inv.supplierID)
	}
	if inv.status != StatusHeld {
		return SubmitResult{}, newError(KindInvalidState,
			"发票 %s 状态为 %s，仅被保留发票可重新判定", number, inv.status)
	}
	res := s.evaluate(at, inv)
	s.st.advance(at)
	return res, nil
}

func validateInvoiceArgs(number, supplierID string, lines []InvoiceLine) *Error {
	if number == "" {
		return newError(KindInvalidParam, "发票号不能为空")
	}
	if supplierID == "" {
		return newError(KindInvalidParam, "发票 %s 供应商不能为空", number)
	}
	if len(lines) == 0 {
		return newError(KindInvalidParam, "发票 %s 至少需要一行", number)
	}
	for i, ln := range lines {
		if ln.POID == "" {
			return newError(KindInvalidParam, "发票 %s 行 %d 订单ID为空", number, i)
		}
		if ln.LineIndex < 0 {
			return newError(KindInvalidParam, "发票 %s 行 %d 行下标为负: %d", number, i, ln.LineIndex)
		}
		if ln.Quantity <= 0 {
			return newError(KindInvalidParam, "发票 %s 行 %d 数量必须为正: %d", number, i, ln.Quantity)
		}
		if ln.UnitPrice <= 0 {
			return newError(KindInvalidParam, "发票 %s 行 %d 单价必须为正: %d", number, i, ln.UnitPrice)
		}
		if _, ok := mulCheckedBig(ln.Quantity, ln.UnitPrice); !ok {
			return newError(KindInvalidParam, "发票 %s 行 %d 金额溢出 int64", number, i)
		}
	}
	if total, ok := invoiceLinesTotal(lines); !ok {
		return newError(KindInvalidParam, "发票 %s 合计金额溢出 int64", number)
	} else {
		_ = total
	}
	return nil
}

// validateInvoiceLines 在持有锁时做结构性校验，按下标顺序返回首个
// 结构性失败行：订单/行不存在(KindLineNotFound)优先于该订单属于其他
// 供应商(KindSupplierMismatch)。规范的提交拒绝次序将“供应商不一致”
// 置于逐行业务判定之前。
func (s *Service) validateInvoiceLines(supplierID string, lines []InvoiceLine) *Error {
	firstPO := ""
	for i, ln := range lines {
		po, ok := s.st.pos[ln.POID]
		if !ok {
			return newError(KindLineNotFound,
				"发票行 %d 引用的订单不存在: %s", i, ln.POID)
		}
		if int(ln.LineIndex) >= len(po.lines) {
			return newError(KindLineNotFound,
				"发票行 %d 引用订单 %s 的行下标越界: %d", i, ln.POID, ln.LineIndex)
		}
		if po.def.SupplierID != supplierID {
			return newError(KindSupplierMismatch,
				"发票行 %d 的订单 %s 属于供应商 %s，与发票供应商 %s 不一致",
				i, ln.POID, po.def.SupplierID, supplierID)
		}
		if firstPO == "" {
			firstPO = ln.POID
		} else if ln.POID != firstPO {
			// 一张发票只能引用一张采购订单：折扣期/账期来自订单条款，
			// 跨订单会让付款额与逾期判定失去唯一依据。
			return newError(KindInvalidParam,
				"发票行 %d 引用订单 %s，与首行订单 %s 不同（发票仅限同一订单）",
				i, ln.POID, firstPO)
		}
	}
	return nil
}

func invoiceLinesTotal(lines []InvoiceLine) (int64, bool) {
	var total int64
	for _, ln := range lines {
		amt, _ := mulCheckedBig(ln.Quantity, ln.UnitPrice)
		t, ok := addChecked(total, amt)
		if !ok {
			return 0, false
		}
		total = t
	}
	return total, true
}

// evaluate 在持有锁时对（新建立的保留或既有保留）发票做一次完整判定。
// 它不推进时钟、不感知冻结（调用方负责），并保证：
//   - 放行：原子地占用全部行额度、写放行金额与放行时刻；
//   - 保留：不触碰任何 poLineState 额度，仅记录首个失败行与原因。
//
// 复杂度：O(m)，m 为发票行数。每步只做 map 查找与常数运算，
// 与订单总数/收货记录数/历史发票数无关。为保证“失败不占额度”，
// 采用两阶段：先在局部候选额度上模拟全部行，全通过后再提交。
func (s *Service) evaluate(at int64, inv *invoiceState) SubmitResult {
	type candidate struct {
		line *poLineState
		qty  int64
	}
	cands := make([]candidate, 0, len(inv.lines))
	var amount int64

	for i, ln := range inv.lines {
		po := s.st.pos[ln.POID] // 结构性校验已保证存在
		line := po.lines[int(ln.LineIndex)]

		// 行内原因优先级：订单行不存在 > 价格不符 > 超开。
		// 订单行存在性已在结构校验阶段保证，这里直接价格检查。
		allow := mulFloorDiv(line.def.UnitPrice, po.def.PriceTolerancePermil)
		diff := ln.UnitPrice - line.def.UnitPrice
		if diff < 0 {
			diff = -diff
		}
		if diff > allow {
			s.emit("invoice.line_fail", map[string]any{
				"invoice": inv.number, "slot": i, "reason": KindPriceMismatch.String(),
				"po_price": line.def.UnitPrice, "inv_price": ln.UnitPrice,
				"allowance": allow, "diff": diff,
			})
			return s.hold(inv, i, KindPriceMismatch, SubmitResult{
				Number: inv.number, Status: StatusHeld, FailLine: i,
				FailReason: KindPriceMismatch,
			})
		}
		// 数量检查以减法比较，避免加法溢出：
		// invoiced + qty > received 等价于 qty > received - invoiced。
		if ln.Quantity > line.received-line.invoicedQty {
			s.emit("invoice.line_fail", map[string]any{
				"invoice": inv.number, "slot": i, "reason": KindOverInvoiced.String(),
				"qty": ln.Quantity, "received": line.received,
				"invoiced":  line.invoicedQty,
				"available": line.received - line.invoicedQty,
			})
			return s.hold(inv, i, KindOverInvoiced, SubmitResult{
				Number: inv.number, Status: StatusHeld, FailLine: i,
				FailReason: KindOverInvoiced,
			})
		}
		// 金额溢出已在 validateInvoiceArgs 阶段拦截。
		lineAmt, _ := mulCheckedBig(ln.Quantity, ln.UnitPrice)
		amount, _ = addChecked(amount, lineAmt)
		cands = append(cands, candidate{line: line, qty: ln.Quantity})
	}

	// 全部通过：提交额度占用。
	for _, c := range cands {
		c.line.invoicedQty += c.qty
	}
	inv.status = StatusReleased
	inv.amount = amount
	inv.releasedAt = at
	inv.failLine = -1
	inv.failReason = 0
	s.emit("invoice.released", map[string]any{
		"invoice": inv.number, "at": at, "amount": amount, "slots": len(inv.lines),
	})
	return SubmitResult{
		Number:     inv.number,
		Status:     StatusReleased,
		Amount:     amount,
		ReleasedAt: at,
		FailLine:   -1,
	}
}

// hold 记录保留判定结果。不改变任何订单行额度。
func (s *Service) hold(inv *invoiceState, lineIdx int, reason ErrorKind, res SubmitResult) SubmitResult {
	inv.status = StatusHeld
	inv.amount = 0
	inv.failLine = lineIdx
	inv.failReason = reason
	return res
}

// mulCheckedBig 先用 int64 快速乘法，溢出时用 big.Int 验证乘积是否仍
// 可表示为 int64。
func mulCheckedBig(a, b int64) (int64, bool) {
	if r, ok := mulChecked(a, b); ok {
		return r, true
	}
	bi := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if bi.IsInt64() {
		return bi.Int64(), true
	}
	return 0, false
}

// GetInvoice 返回发票只读快照；不存在返回 KindNotFound。查询不推进时钟。
func (s *Service) GetInvoice(number string) (InvoiceSnapshot, *Error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	inv, ok := s.st.invoices[number]
	if !ok {
		return InvoiceSnapshot{}, newError(KindNotFound, "发票不存在: %s", number)
	}
	return inv.snapshot(), nil
}
