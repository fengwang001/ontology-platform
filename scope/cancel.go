package scope

// Cancel 以给定原因显式结束作用域，并向所有后代连坐传播。
//
// 结束是一次性的：若作用域已经结束（含刚超时），本次调用不产生
// 任何效果，首次判定的 Err/Reason/Origin 保持不变。
func (s *Scope) Cancel(r Reason) {
	s.finish(ErrCanceled, r)
}
