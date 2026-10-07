package reflog

// expireLocked physically removes expired records from every log.
// The caller must hold the write lock and must have validated now.
func (s *System) expireLocked(now int64) int {
	total := 0
	for _, l := range s.logs {
		total += l.expire(now, s.cfg)
	}
	return total
}
