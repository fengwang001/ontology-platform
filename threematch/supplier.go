package threematch

import "math"

// supplierState 为供应商运行态。
type supplierState struct {
	id     string
	frozen bool
}

// RegisterSupplier 注册供应商。重复注册按参数非法处理（同一调用入口内
// 无法区分“对象已存在”这一独立优先级，故归入非法请求）。
func (s *Service) RegisterSupplier(at int64, id string) *Error {
	if id == "" {
		return newError(KindInvalidParam, "供应商ID不能为空")
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if err := s.st.checkClock(at); err != nil {
		return err
	}
	if _, ok := s.st.suppliers[id]; ok {
		return newError(KindInvalidParam, "供应商已存在: %s", id)
	}
	s.st.suppliers[id] = &supplierState{id: id}
	s.st.advance(at)
	return nil
}

// FreezeSupplier 冻结供应商。冻结是幂等操作：重复冻结仍然成功，
// 因为它不改变“已冻结”这一状态语义，且测试需要稳定的冻结语义。
func (s *Service) FreezeSupplier(at int64, id string) *Error {
	return s.setFrozen(at, id, true, false)
}

// UnfreezeSupplier 解冻供应商，同样幂等。
func (s *Service) UnfreezeSupplier(at int64, id string) *Error {
	return s.setFrozen(at, id, false, true)
}

func (s *Service) setFrozen(at int64, id string, frozen bool, _ bool) *Error {
	if id == "" {
		return newError(KindInvalidParam, "供应商ID不能为空")
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if err := s.st.checkClock(at); err != nil {
		return err
	}
	sup, ok := s.st.suppliers[id]
	if !ok {
		return newError(KindNotFound, "供应商不存在: %s", id)
	}
	sup.frozen = frozen
	s.st.advance(at)
	return nil
}

// IsFrozen 查询供应商冻结状态；供应商不存在返回 KindNotFound。
// 只读查询不参与时钟推进，可在任意时刻调用。
func (s *Service) IsFrozen(id string) (bool, *Error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	sup, ok := s.st.suppliers[id]
	if !ok {
		return false, newError(KindNotFound, "供应商不存在: %s", id)
	}
	return sup.frozen, nil
}

// validatePermil 校验千分比取值范围 [0,1000]。
func validatePermil(name string, v int64) *Error {
	if v < 0 || v > 1000 {
		return newError(KindInvalidParam, "%s 千分比必须在 [0,1000]: %d", name, v)
	}
	return nil
}

// mulChecked 计算 a*b 并以 ok=false 报告溢出。所有输入量均限制在
// int64 正数空间内，乘法是唯一可能溢出的运算（加法用 addChecked）。
func mulChecked(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	r := a * b
	if r/b != a || (r == math.MinInt64) {
		return 0, false
	}
	return r, true
}

func addChecked(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}
