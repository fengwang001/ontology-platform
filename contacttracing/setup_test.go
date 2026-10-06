package contacttracing

// setupSecondary 构造 p(病例) - q(密接) - z(次密接)：
// q 与 p 在传染期同室 120 分钟 [1000,1120)；
// z 与 q 在 q 暴露期（起点 1000，到确诊登记 now 不含）同室 120 分钟 [4880,5000)。
func setupSecondary(s *System, now int64) error {
	if err := s.RecordStay("p", "R1", now, 1000, 1120); err != nil {
		return err
	}
	if err := s.RecordStay("q", "R1", now, 1000, 1120); err != nil {
		return err
	}
	if err := s.RecordStay("q", "R2", now, 4880, 5000); err != nil {
		return err
	}
	if err := s.RecordStay("z", "R2", now, 4880, 5000); err != nil {
		return err
	}
	_, err := s.RegisterCase("p", 5000, 2000)
	return err
}
