package threematch

// Service 是三单匹配与付款放行系统的并发安全入口。
type Service struct {
	st *store
}

// NewService 创建空系统。
func NewService() *Service {
	return &Service{st: newStore()}
}

// SetTracer 注入判定跟踪钩子（nil 关闭）。钩子在持有内部锁时同步调用，
// 不得在其中再次调用 Service 方法，否则会死锁。
func (s *Service) SetTracer(t func(event string, detail map[string]any)) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	s.st.trace = t
}

func (s *Service) emit(event string, detail map[string]any) {
	if s.st.trace != nil {
		s.st.trace(event, detail)
	}
}
