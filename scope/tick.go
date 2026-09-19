package scope

// deadlineReason 是自身截止时间到达时使用的默认原因文字。
const deadlineReason Reason = "deadline exceeded"

// Tick 按当前注入时钟重新判定截止时间，并沿树向下传播。
//
// 判定为左闭右开：now 严格早于 deadline 时未结束；now >= deadline
// 即判定为自身超时。无论自身是否超时，都会继续对后代执行 Tick，
// 使后代更紧的自身截止时间也能被同一时钟触发。
// 不使用任何真实时间设施（time.After / time.Now 等）。
func (s *Scope) Tick() {
	s.mu.Lock()
	dl := s.deadline
	ended := s.ended
	kids := make([]*Scope, 0, len(s.children))
	for kid := range s.children {
		kids = append(kids, kid)
	}
	s.mu.Unlock()

	if !ended && !dl.IsZero() && !s.now().Before(dl) {
		s.finish(ErrDeadlineExceeded, deadlineReason)
	}

	for _, kid := range kids {
		kid.Tick()
	}
}
