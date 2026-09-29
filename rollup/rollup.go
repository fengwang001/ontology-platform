package rollup

import (
	"fmt"
	"sync"
)

// DefaultMaxDetailGroups 为明细组数默认上限。
const DefaultMaxDetailGroups = 100000

type groupState struct {
	count int64
	sum   int64
}

// Store 并发安全地维护明细组、第一维小计、总计三层状态及变更日志。
type Store struct {
	mu sync.RWMutex

	maxDetailGroups int
	rows            map[string]Row
	detail          map[GroupKey]*groupState
	subtotal        map[GroupKey]*groupState
	grand           groupState
	log             []LogEntry
}

// New 创建 Store，maxGroups<=0 时使用默认上限。
func New(maxGroups int) *Store {
	if maxGroups <= 0 {
		maxGroups = DefaultMaxDetailGroups
	}
	return &Store{
		maxDetailGroups: maxGroups,
		rows:            make(map[string]Row),
		detail:          make(map[GroupKey]*groupState),
		subtotal:        make(map[GroupKey]*groupState),
	}
}

// Apply 校验并应用一条增量；被整体拒绝时不留下任何状态或日志痕迹。
func (s *Store) Apply(inc Increment) (LogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if inc.Op != OpAdd && inc.Op != OpRemove {
		return LogEntry{}, ErrInvalidIncrement
	}
	if inc.Row.ID == "" {
		return LogEntry{}, ErrInvalidIncrement
	}
	_, exists := s.rows[inc.Row.ID]
	switch inc.Op {
	case OpAdd:
		if exists {
			return LogEntry{}, ErrDuplicateRow
		}
	case OpRemove:
		if !exists {
			return LogEntry{}, ErrRowNotFound
		}
	}

	entry := LogEntry{Seq: int64(len(s.log)) + 1, Op: inc.Op, Row: inc.Row}

	var deltaCount, deltaSum int64
	var dk, sk GroupKey
	// 撤回只需行标识；分组键与数值以当前确实存在的已存行为准，
	// 从而保证负变更精确抵消该加入时产生的正变更。
	effective := inc.Row
	if inc.Op == OpRemove {
		effective = s.rows[inc.Row.ID]
	}
	d1v, hasD1 := keyDim(effective.Dim1)
	d2v, hasD2 := keyDim(effective.Dim2)
	dk = GroupKey{Layer: LayerDetail, Dim1: d1v, HasDim1: hasD1, Dim2: d2v, HasDim2: hasD2}
	sk = GroupKey{Layer: LayerSubtotal, Dim1: d1v, HasDim1: hasD1}
	gk := GroupKey{Layer: LayerGrand}

	if inc.Op == OpAdd {
		deltaCount, deltaSum = 1, inc.Row.Value
		if _, ok := s.detail[dk]; !ok {
			if len(s.detail) >= s.maxDetailGroups {
				return LogEntry{}, ErrTooManyGroups
			}
		}
	} else {
		deltaCount, deltaSum = -1, -effective.Value
	}

	entry.Changes[0] = s.applyLayer(s.detail, dk, deltaCount, deltaSum, LayerDetail)
	entry.Changes[1] = s.applyLayer(s.subtotal, sk, deltaCount, deltaSum, LayerSubtotal)
	entry.Changes[2] = s.applyGrand(gk, deltaCount, deltaSum)

	if inc.Op == OpAdd {
		s.rows[inc.Row.ID] = inc.Row
	} else {
		delete(s.rows, inc.Row.ID)
	}

	s.log = append(s.log, entry)
	return entry, nil
}

func (s *Store) applyLayer(m map[GroupKey]*groupState, k GroupKey, dc, ds int64, layer Layer) Change {
	g := m[k]
	if g == nil {
		g = &groupState{}
		m[k] = g
	}
	g.count += dc
	g.sum += ds
	if g.count == 0 {
		delete(m, k)
	}
	return Change{
		Layer:       layer,
		Key:         k,
		CountDelta:  dc,
		SumDelta:    ds,
		ResultCount: g.count,
		ResultSum:   g.sum,
	}
}

func (s *Store) applyGrand(k GroupKey, dc, ds int64) Change {
	s.grand.count += dc
	s.grand.sum += ds
	return Change{
		Layer:       LayerGrand,
		Key:         k,
		CountDelta:  dc,
		SumDelta:    ds,
		ResultCount: s.grand.count,
		ResultSum:   s.grand.sum,
	}
}

// DetailGroups 按确定性顺序返回当前全部非空明细组。
func (s *Store) DetailGroups() []GroupView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return snapshotGroups(s.detail)
}

// Subtotals 按确定性顺序返回当前全部非空第一维小计。
func (s *Store) Subtotals() []GroupView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return snapshotGroups(s.subtotal)
}

// Total 返回总计。
func (s *Store) Total() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{Count: s.grand.count, Sum: s.grand.sum}
}

// Rows 返回当前行集的确定性顺序快照。
func (s *Store) Rows() []Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Row, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, r)
	}
	sortRows(out)
	return out
}

// Log 返回已提交变更日志的拷贝。
func (s *Store) Log() []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]LogEntry, len(s.log))
	copy(out, s.log)
	return out
}

// Verify 自检：三层恒等式、日志重放一致性；不一致返回描述性错误。
func (s *Store) Verify() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.grand.count < 0 {
		return fmt.Errorf("grand count negative: %d", s.grand.count)
	}

	var detailCount, detailSum int64
	subAgg := make(map[GroupKey]groupState, len(s.subtotal))
	for k, g := range s.detail {
		if g.count <= 0 {
			return fmt.Errorf("detail group %v retained with non-positive count %d", k, g.count)
		}
		detailCount += g.count
		detailSum += g.sum
		sk := GroupKey{Layer: LayerSubtotal, Dim1: k.Dim1, HasDim1: k.HasDim1}
		agg := subAgg[sk]
		agg.count += g.count
		agg.sum += g.sum
		subAgg[sk] = agg
	}
	if detailCount != s.grand.count || detailSum != s.grand.sum {
		return fmt.Errorf("detail total {count:%d sum:%d} != grand {count:%d sum:%d}",
			detailCount, detailSum, s.grand.count, s.grand.sum)
	}

	var subCount, subSum int64
	for k, g := range s.subtotal {
		if g.count <= 0 {
			return fmt.Errorf("subtotal %v retained with non-positive count %d", k, g.count)
		}
		subCount += g.count
		subSum += g.sum
		agg, ok := subAgg[k]
		if !ok || agg != *g {
			return fmt.Errorf("subtotal %v {count:%d sum:%d} != sum of detail groups", k, g.count, g.sum)
		}
	}
	if len(subAgg) != len(s.subtotal) {
		return fmt.Errorf("subtotal layer has %d groups but detail aggregates to %d",
			len(s.subtotal), len(subAgg))
	}
	if subCount != s.grand.count || subSum != s.grand.sum {
		return fmt.Errorf("subtotal total {count:%d sum:%d} != grand {count:%d sum:%d}",
			subCount, subSum, s.grand.count, s.grand.sum)
	}

	if int64(len(s.rows)) != s.grand.count {
		return fmt.Errorf("row set size %d != grand count %d", len(s.rows), s.grand.count)
	}

	replayed := NewReplay()
	for i := range s.log {
		replayed.ApplyEntry(s.log[i])
		if err := replayed.checkInvariant(); err != nil {
			return fmt.Errorf("log prefix through seq %d not self-consistent: %w", i+1, err)
		}
	}
	if !groupsEqual(replayed.detail, s.detail) ||
		!groupsEqual(replayed.subtotal, s.subtotal) ||
		replayed.grand != s.grand {
		return fmt.Errorf("full log replay does not reproduce current state")
	}

	return nil
}
