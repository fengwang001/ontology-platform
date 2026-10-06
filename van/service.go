package van

import (
	"sort"
	"sync"
)

// ZoneSpec 分区容量定义（编号由切片下标 +1 决定，从车头到车尾）。
type ZoneSpec struct {
	WeightLimit int
	VolumeLimit int
}

// Remaining 分区剩余容量。
type Remaining struct {
	Weight int
	Volume int
}

// BatchError 批量装货失败：Index 为最小下标失败件（0 起），Err 为原因。
type BatchError struct {
	Index int
	Err   error
}

func (e *BatchError) Error() string { return e.Err.Error() }
func (e *BatchError) Unwrap() error { return e.Err }

// Service 配载与卸货顺序校验系统。
type Service struct {
	mu      sync.RWMutex
	zones   []*zone
	loc     map[int]int
	arrived int
}

// New 依据从车头到车尾排列的分区容量定义构造系统。
func New(specs []ZoneSpec) *Service {
	if len(specs) == 0 {
		panic("van: at least one zone required")
	}
	zones := make([]*zone, len(specs))
	for i, sp := range specs {
		if sp.WeightLimit <= 0 || sp.VolumeLimit <= 0 {
			panic("van: zone limits must be positive")
		}
		zones[i] = newZone(sp.WeightLimit, sp.VolumeLimit)
	}
	return &Service{zones: zones, loc: make(map[int]int)}
}

func validCargo(c Cargo) error {
	if c.ID <= 0 || c.Weight <= 0 || c.Volume <= 0 || c.Stop <= 0 {
		return ErrInvalid
	}
	if c.Kind < General || c.Kind > Food {
		return ErrInvalid
	}
	return nil
}

// chooseZone 在已加锁状态下选择编号最小的可行分区。
// 无可行分区时返回 0 与归并后的整体失败原因：
// 各分区取其首个不满足约束（顺序 > 隔离 > 超重 > 超容），
// 整体取其中严重度最低的一类，故只要有分区仅超容即报超容。
func (s *Service) chooseZone(c Cargo) (int, RejectReason) {
	worst := constraintNone
	for idx, z := range s.zones {
		if s.globalOrderViolation(idx, c) {
			if constraintOrder > worst {
				worst = constraintOrder
			}
			continue
		}
		v := z.firstViolation(c)
		if v == constraintNone {
			return idx + 1, ReasonOrder
		}
		if v > worst {
			worst = v
		}
	}
	return 0, rejectFromConstraint(worst)
}

func rejectFromConstraint(v constraint) RejectReason {
	switch v {
	case constraintOrder:
		return ReasonOrder
	case constraintIsolation:
		return ReasonIsolation
	case constraintOverWeight:
		return ReasonOverWeight
	default:
		return ReasonOverVolume
	}
}

// globalOrderViolation 判定候选货物放入分区 idx 是否违反跨分区顺序。
// 推导（分区1为车头，早卸货须靠车尾）：
//   - idx 之前(更靠前)的分区中，所有货物必须不比候选更早卸，即前缀最小停靠点 >= c.Stop；
//   - idx 之后(更靠后)的分区中，所有货物必须不比候选更晚卸，即后缀最大停靠点 <= c.Stop。
//
// 同区约束由 zone.firstViolation 用 maxStop 单独判定。
// 仅读各分区 minStop/maxStop 两个标量，成本 O(Z)，与车上货物总数 N 无关。
func (s *Service) globalOrderViolation(idx int, c Cargo) bool {
	for i := 0; i < idx; i++ {
		z := s.zones[i]
		if !z.empty() && z.minStop < c.Stop {
			return true
		}
	}
	for i := idx + 1; i < len(s.zones); i++ {
		z := s.zones[i]
		if !z.empty() && z.maxStop > c.Stop {
			return true
		}
	}
	return false
}

// Load 装入单件货物，返回确定的分区编号（编号最小的可行分区）。
func (s *Service) Load(c Cargo) (int, error) {
	if err := validCargo(c); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.loc[c.ID]; ok {
		return 0, ErrDuplicate
	}
	if s.arrived > 0 && c.Stop <= s.arrived {
		return 0, ErrStopPassed
	}
	zoneNo, reason := s.chooseZone(c)
	if zoneNo == 0 {
		return 0, &RejectError{Reason: reason}
	}
	s.zones[zoneNo-1].put(c)
	s.loc[c.ID] = zoneNo
	return zoneNo, nil
}

// LoadBatch 按给定次序逐件装入，全有或全无。
// 批内编号重复为参数非法；任一前序校验失败均不产生副作用。
func (s *Service) LoadBatch(cs []Cargo) (map[int]int, error) {
	for _, c := range cs {
		if err := validCargo(c); err != nil {
			return nil, err
		}
	}
	seen := make(map[int]struct{}, len(cs))
	for _, c := range cs {
		if _, dup := seen[c.ID]; dup {
			return nil, ErrInvalid
		}
		seen[c.ID] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	type placement struct {
		zoneNo int
		cargo  Cargo
	}
	done := make([]placement, 0, len(cs))
	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			p := done[i]
			s.zones[p.zoneNo-1].remove(p.cargo.ID)
			delete(s.loc, p.cargo.ID)
		}
	}

	for i, c := range cs {
		if _, ok := s.loc[c.ID]; ok {
			rollback()
			return nil, &BatchError{Index: i, Err: ErrDuplicate}
		}
		if s.arrived > 0 && c.Stop <= s.arrived {
			rollback()
			return nil, &BatchError{Index: i, Err: ErrStopPassed}
		}
		zoneNo, reason := s.chooseZone(c)
		if zoneNo == 0 {
			rollback()
			return nil, &BatchError{Index: i, Err: &RejectError{Reason: reason}}
		}
		s.zones[zoneNo-1].put(c)
		s.loc[c.ID] = zoneNo
		done = append(done, placement{zoneNo: zoneNo, cargo: c})
	}

	result := make(map[int]int, len(cs))
	for _, p := range done {
		result[p.cargo.ID] = p.zoneNo
	}
	return result, nil
}

// Unload 到达某停靠点并卸下该停靠点全部货物。
// stop <= 已到达最大序号：报已处理；越级且车上仍有更小序号货物：顺序错误；
// 空停靠点成功并推进进度。
func (s *Service) Unload(stop int) ([]int, error) {
	if stop <= 0 {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if stop <= s.arrived {
		return nil, ErrProcessed
	}
	minOnBoard := 0
	for _, z := range s.zones {
		if !z.empty() && (minOnBoard == 0 || z.minStop < minOnBoard) {
			minOnBoard = z.minStop
		}
	}
	if minOnBoard != 0 && minOnBoard < stop {
		return nil, ErrOrderError
	}
	removed := make([]int, 0)
	for _, z := range s.zones {
		for id, c := range z.items {
			if c.Stop == stop {
				removed = append(removed, id)
			}
		}
	}
	for _, id := range removed {
		zoneNo := s.loc[id]
		s.zones[zoneNo-1].remove(id)
		delete(s.loc, id)
	}
	s.arrived = stop
	sort.Ints(removed)
	return removed, nil
}

// Remaining 返回各分区（编号 1 起）剩余载重与剩余容积，为一致快照副本。
func (s *Service) Remaining() []Remaining {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Remaining, len(s.zones))
	for i, z := range s.zones {
		out[i] = Remaining{
			Weight: z.weightLimit - z.weight,
			Volume: z.volumeLimit - z.volume,
		}
	}
	return out
}

// Location 返回某货物当前所在分区编号；不在车上返回 ErrNotFound。
func (s *Service) Location(cargoID int) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if zoneNo, ok := s.loc[cargoID]; ok {
		return zoneNo, nil
	}
	return 0, ErrNotFound
}

// ArrivedStop 返回已到达的最大停靠点序号；未开始卸货为 0。
func (s *Service) ArrivedStop() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.arrived
}
