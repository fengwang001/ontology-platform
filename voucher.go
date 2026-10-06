package ontology

import "fmt"

// createVoucher 在已持锁状态下生成一张代金券。
// ID 由单调序号决定，保证相同操作序列重放得到相同标识。
// 调用方保证 amount > 0。
func (s *System) createVoucher(owner string, amount int64, now int64) *Voucher {
	s.vcSeq++
	v := &Voucher{
		ID:        fmt.Sprintf("V%06d", s.vcSeq),
		Owner:     owner,
		Amount:    amount,
		CreatedAt: now,
		ExpiresAt: now + s.cfg.VoucherTTL,
	}
	s.vouchers[v.ID] = v
	return v
}

// validateVoucher 按题目规定的代金券错误次序校验：
// 不存在 > 归属不符 > 已过期（恰等于到期时刻视为过期）> 已用尽。
// 必须在已持锁状态下调用。
func (s *System) validateVoucher(id string, owner string, now int64) (*Voucher, error) {
	v := s.vouchers[id]
	if v == nil {
		return nil, errf(KindVoucherNotFound, "voucher not found: %s", id)
	}
	if v.Owner != owner {
		return nil, errf(KindVoucherOwner, "voucher %s owner mismatch", id)
	}
	if now >= v.ExpiresAt {
		return nil, errf(KindVoucherExpired, "voucher %s expired at %d (now %d)", id, v.ExpiresAt, now)
	}
	if v.Used || v.Amount <= 0 {
		return nil, errf(KindVoucherUsed, "voucher %s already used up", id)
	}
	return v, nil
}

// consumeVoucher 从券面额中核减 amount（amount <= v.Amount）。
// 面额大于应补总额时差额留在原券上，不生成新券。
// 必须在已持锁状态下调用。
func consumeVoucher(v *Voucher, amount int64) int64 {
	v.Amount -= amount
	if v.Amount <= 0 {
		v.Amount = 0
		v.Used = true
	}
	return v.Amount
}
