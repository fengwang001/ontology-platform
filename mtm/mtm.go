package mtm

import "ontology/trade"

// ErrInvalid 与 trade.ErrInvalid 为同一错误，便于 errors.Is 区分。
// Settle 以 sp 对 sym 全部持仓日终结算，返回追保账户名单（字节序）。
// 流程：参数校验 → BeginSettle（时钟回退、合约不存在，原子且拒绝不落时钟）
// → 对每个仓位独立计算盯市盈亏并入账、今仓并入昨仓 → 返回追保名单。
func Settle(e *trade.Engine, now int64, sym []byte, sp int64) ([][]byte, error) {
	if now < 0 || now > 1e12 || len(sym) == 0 || sp < 1 || sp > 1e7 {
		return nil, trade.ErrInvalid
	}
	sdef, err := e.BeginSettle(now, sym)
	if err != nil {
		return nil, err
	}
	for _, v := range e.PositionsOf(sym) {
		pnl := settlePnL(v, sdef.Mult, sp)
		e.ApplySettle(v.Key, pnl, sp)
	}
	return e.MarginCalls(), nil
}

// settlePnL 按视图计算盯市盈亏，保证 mtm 不依赖 lot 内部表示。
func settlePnL(v trade.PosView, mult, sp int64) int64 {
	sign := int64(v.Key.Dir)
	pnl := sign * (sp - v.SP0) * v.QY * mult
	for _, b := range v.Today {
		if b.Qty > 0 {
			pnl += sign * (sp - b.Price) * b.Qty * mult
		}
	}
	return pnl
}
