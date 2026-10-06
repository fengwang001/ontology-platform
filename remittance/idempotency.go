package remittance

// idemEntry 记录一次被接受提交的参数与结果快照。
type idemEntry struct {
	remitter string
	quoteID  string
	payee    string
	result   SubmitResult
}

// idemStore 为幂等键存储。被拒绝的提交不记录。
type idemStore struct {
	entries map[string]idemEntry
}

func newIdemStore() *idemStore {
	return &idemStore{entries: make(map[string]idemEntry)}
}

// lookup 返回幂等键对应的历史记录。
func (s *idemStore) lookup(key string) (idemEntry, bool) {
	e, ok := s.entries[key]
	return e, ok
}

// save 在被接受的提交成功后记录幂等键。
func (s *idemStore) save(key string, e idemEntry) {
	s.entries[key] = e
}
