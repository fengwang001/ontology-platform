package threematch

// CreatePurchaseOrder 创建采购订单。
//
// 校验顺序遵循总体优先级：参数非法 > 时钟回退 > 对象不存在（供应商）。
// 订单号重复属于同一对象命名空间冲突，归入参数非法。
func (s *Service) CreatePurchaseOrder(at int64, po PurchaseOrder) *Error {
	if err := validatePO(po); err != nil {
		return err
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if err := s.st.checkClock(at); err != nil {
		return err
	}
	if _, ok := s.st.pos[po.ID]; ok {
		return newError(KindInvalidParam, "采购订单号重复: %s", po.ID)
	}
	if _, ok := s.st.suppliers[po.SupplierID]; !ok {
		return newError(KindNotFound, "供应商不存在: %s", po.SupplierID)
	}
	st := &poState{def: po, lines: make([]*poLineState, len(po.Lines))}
	for i, line := range po.Lines {
		st.lines[i] = &poLineState{def: line}
	}
	s.st.pos[po.ID] = st
	s.st.advance(at)
	return nil
}

func validatePO(po PurchaseOrder) *Error {
	if po.ID == "" {
		return newError(KindInvalidParam, "订单ID不能为空")
	}
	if po.SupplierID == "" {
		return newError(KindInvalidParam, "订单供应商不能为空: %s", po.ID)
	}
	if len(po.Lines) == 0 {
		return newError(KindInvalidParam, "订单至少需要一行: %s", po.ID)
	}
	if err := validatePermil("超收容忍", po.OverReceiptPermil); err != nil {
		return err
	}
	if err := validatePermil("价格容差", po.PriceTolerancePermil); err != nil {
		return err
	}
	if err := validatePermil("折扣", po.DiscountPermil); err != nil {
		return err
	}
	if po.PaymentTermSec < 0 {
		return newError(KindInvalidParam, "账期不能为负: %d", po.PaymentTermSec)
	}
	if po.DiscountPeriodSec < 0 {
		return newError(KindInvalidParam, "折扣期不能为负: %d", po.DiscountPeriodSec)
	}
	if po.DiscountPeriodSec > po.PaymentTermSec {
		return newError(KindInvalidParam, "折扣期 %d 不得大于账期 %d", po.DiscountPeriodSec, po.PaymentTermSec)
	}
	for i, line := range po.Lines {
		if line.Quantity <= 0 {
			return newError(KindInvalidParam, "订单行 %d 订购量必须为正: %d", i, line.Quantity)
		}
		if line.UnitPrice <= 0 {
			return newError(KindInvalidParam, "订单行 %d 单价必须为正: %d", i, line.UnitPrice)
		}
	}
	return nil
}

// resolveLine 在持有锁时解析订单行；不存在返回 KindLineNotFound 风格错误，
// 但区分“订单不存在”和“行下标越界”，两者对发票判定都映射为
// KindLineNotFound（行内原因优先级第一项）。
func (s *Service) resolveLine(poID string, idx int64) (*poState, *poLineState, *Error) {
	po, ok := s.st.pos[poID]
	if !ok {
		return nil, nil, newError(KindLineNotFound, "订单不存在: %s", poID)
	}
	if idx < 0 || int(idx) >= len(po.lines) {
		return nil, nil, newError(KindLineNotFound, "订单 %s 行下标越界: %d", poID, idx)
	}
	return po, po.lines[int(idx)], nil
}
