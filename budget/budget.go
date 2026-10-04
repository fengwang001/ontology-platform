// Package budget 是并发安全的隐私额度账目存储：
// 数据集终身账（used/resv）与分析师按窗口占用账。
package budget

import (
	"errors"
	"sort"
	"sync"
)

const (
	maxWindowSec = 1_000_000_000
	maxAmount    = 1_000_000_000_000
	maxNow       = 1_000_000_000_000
)

// 账目的哨兵错误，均可用 errors.Is 区分。
var (
	// ErrExists：数据集或分析师重名登记。
	ErrExists = errors.New("budget: entity already exists")
	// ErrNotFound：查询未知数据集或分析师。
	ErrNotFound = errors.New("budget: entity not found")
	// ErrInvalidAmount：名字为空或额度不在 1..10^12。
	ErrInvalidAmount = errors.New("budget: invalid amount")
	// ErrInvalidTime：now 超出 0..10^12 或小于已接受的最大 now。
	ErrInvalidTime = errors.New("budget: invalid or non-monotonic time")
)

type dataset struct {
	cap  int64
	used int64
	resv int64
}

type analyst struct {
	cap      int64
	windows  map[int64]int64
}

// Store 保存全部账目。零值不可用，须用 New 构造。
type Store struct {
	mu       sync.RWMutex
	wn       int64
	now      int64
	datasets map[string]*dataset
	analysts map[string]*analyst
}

// New 以窗口长度（秒）构造账目存储。
// windowSec 不在 1..10^9 时 panic（构造参数属于不可恢复的编程错误）。
func New(windowSec int64) *Store {
	if windowSec < 1 || windowSec > maxWindowSec {
		panic("budget: window length out of range")
	}
	return &Store{
		wn:       windowSec,
		datasets: map[string]*dataset{},
		analysts: map[string]*analyst{},
	}
}

// AddDataset 登记数据集及其终身额度。
func (s *Store) AddDataset(name string, capB int64) error {
	if name == "" || capB < 1 || capB > maxAmount {
		return ErrInvalidAmount
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.datasets[name]; ok {
		return ErrExists
	}
	s.datasets[name] = &dataset{cap: capB}
	return nil
}

// AddAnalyst 登记分析师及其每窗口额度。
func (s *Store) AddAnalyst(name string, capA int64) error {
	if name == "" || capA < 1 || capA > maxAmount {
		return ErrInvalidAmount
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.analysts[name]; ok {
		return ErrExists
	}
	s.analysts[name] = &analyst{cap: capA, windows: map[int64]int64{}}
	return nil
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// CheckClock 在锁外入口复用：now 越界或回退返回 ErrInvalidTime。
func (s *Store) CheckClock(now int64) error {
	if !validNow(now) {
		return ErrInvalidTime
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if now < s.now {
		return ErrInvalidTime
	}
	return nil
}

// AdvanceClock 单调推进已接受时钟（now == 当前值允许）。
func (s *Store) AdvanceClock(now int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now > s.now {
		s.now = now
	}
}

// HasDataset / HasAnalyst 判断实体是否存在。
func (s *Store) HasDataset(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.datasets[name]
	return ok
}

func (s *Store) HasAnalyst(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.analysts[name]
	return ok
}

// Window 返回 now 所属窗口号 floor(now/Wn)。
func (s *Store) Window(now int64) int64 { return now / s.wn }

// DatasetRemaining 返回 Bd-used-resv；未知数据集返回 ErrNotFound。
func (s *Store) DatasetRemaining(name string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.datasets[name]
	if !ok {
		return 0, ErrNotFound
	}
	return d.cap - d.used - d.resv, nil
}

// DatasetUsed 仅用于测试/朴素对照：返回已用额度。
func (s *Store) DatasetUsed(name string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.datasets[name]
	if !ok {
		return 0, ErrNotFound
	}
	return d.used, nil
}

// DatasetResv 仅用于测试/朴素对照：返回在途预留额度。
func (s *Store) DatasetResv(name string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.datasets[name]
	if !ok {
		return 0, ErrNotFound
	}
	return d.resv, nil
}

// AnalystWindowUsage 返回分析师某窗口当前占用。
func (s *Store) AnalystWindowUsage(name string, window int64) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.analysts[name]
	if !ok {
		return 0, ErrNotFound
	}
	return a.windows[window], nil
}

// AnalystRemainingAtWindow 返回 Ba 减指定窗口占用。
func (s *Store) AnalystRemainingAtWindow(name string, window int64) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.analysts[name]
	if !ok {
		return 0, ErrNotFound
	}
	return a.cap - a.windows[window], nil
}

// SortedDatasets 仅用于测试对照：按字节序返回已登记数据集名。
func (s *Store) SortedDatasets() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.datasets))
	for name := range s.datasets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SortedAnalysts 仅用于测试对照：按字节序返回已登记分析师名。
func (s *Store) SortedAnalysts() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.analysts))
	for name := range s.analysts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DatasetFits 判断单个数据集在 used+resv 之上再占 cost 是否不超额度（恰等允许）。
func (s *Store) DatasetFits(name string, cost int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.datasets[name]
	return d.used+d.resv+cost <= d.cap
}

// AnalystFits 判断分析师在 window 窗口占用上加 cost 是否不超额度（恰等允许）。
func (s *Store) AnalystFits(name string, window, cost int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a := s.analysts[name]
	return a.windows[window]+cost <= a.cap
}

// ReserveDatasets 给一批数据集的在途预留各加 cost（analyst 占用只计一次，
// 由 ReserveAnalyst 另行处理）。调用方须已确认全部够额与名字互不相同。
func (s *Store) ReserveDatasets(names []string, cost int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range names {
		d := s.datasets[name]
		d.resv += cost
	}
}

// ReserveAnalyst 给分析师某窗口占用加 cost（一次预留只加一次）。
func (s *Store) ReserveAnalyst(name string, window, cost int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.analysts[name]
	a.windows[window] += cost
}

// CancelReserve 全额退还一次预留：数据集 resv 减 cost、窗口占用减 cost。
func (s *Store) CancelReserve(names []string, analyst string, window, cost int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range names {
		s.datasets[name].resv -= cost
	}
	a := s.analysts[analyst]
	a.windows[window] -= cost
	if a.windows[window] == 0 {
		delete(a.windows, window)
	}
}

// CommitRunning 结算在途预留：数据集 resv 减 cost、used 加 actual；
// 分析师所属窗口占用由 cost 改为 actual。
func (s *Store) CommitRunning(names []string, analyst string, window, cost, actual int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range names {
		d := s.datasets[name]
		d.resv -= cost
		d.used += actual
	}
	a := s.analysts[analyst]
	a.windows[window] += actual - cost
	if a.windows[window] == 0 {
		delete(a.windows, window)
	}
}

// FinalizeRunningCancel 撤销在跑查询：数据集 resv 减 cost、used 加 cost（全额计入）；
// 分析师占用保持 cost 不动。
func (s *Store) FinalizeRunningCancel(names []string, cost int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range names {
		d := s.datasets[name]
		d.resv -= cost
		d.used += cost
	}
}
