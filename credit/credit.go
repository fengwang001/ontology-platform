package credit

import "sync"

// Store 爽约记录存储。每位患者的记录时刻按非降序排列（全局时钟不回退）。
type Store struct {
	k       int
	w       int64
	mu      sync.RWMutex
	records map[string][]int64
	touched int
}

// New 创建记录存储。k 为禁约阈值，w 为禁约窗口。
func New(k int, w int64) *Store {
	return &Store{k: k, w: w, records: map[string][]int64{}}
}

// Add 追加一条时刻为 t 的爽约记录。
func (s *Store) Add(patient []byte, t int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := string(patient)
	s.records[key] = append(s.records[key], t)
}

// PopLast 删除该患者最新一条爽约记录，仅供 booking 事务回滚使用。
func (s *Store) PopLast(patient []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := string(patient)
	rec := s.records[key]
	if len(rec) > 0 {
		s.records[key] = rec[:len(rec)-1]
	}
}

// Records 返回该患者爽约时刻的只读副本（供状态核对与测试断言）。
func (s *Store) Records(patient []byte) []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]int64(nil), s.records[string(patient)]...)
}

// Banned 判定 now 时患者是否被禁约：窗口内（now-t < w，恰等 now-w 不计）
// 记录数不少于 k。只读，不推进任何状态。
// 从最新记录向前读，满 k 条即停，读取条数不超过 k。
func (s *Store) Banned(now int64, patient []byte) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec := s.records[string(patient)]
	s.touched = 0
	cutoff := now - s.w
	count := 0
	for i := len(rec) - 1; i >= 0; i-- {
		s.touched++
		if rec[i] <= cutoff { // 记录非降序：更早的记录必然也在窗口外
			break
		}
		count++
		if count >= s.k {
			return true
		}
	}
	return false
}

// Touched 返回最近一次 Banned 读取的爽约记录条数（非导出计数器，供同包测试）。
func (s *Store) Touched() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.touched
}

// ResetTouched 清零读取计数。
func (s *Store) ResetTouched() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched = 0
}
