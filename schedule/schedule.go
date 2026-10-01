// Package schedule 实现带临时覆盖的值班轮值表。
//
// 轮值规则：名册 members 含 n 个互不相同的成员，起点 T0 与班长 L（分钟）
// 均为 int64。时刻 t 的轮值成员为
//
//	members[floorMod(floorDiv(t-T0, L), n)]
//
// 其中 floorDiv 向下取整、floorMod 结果非负，因此 t < T0 时按同一公式
// 向前外推（t == T0-1 属于最后一个成员）。
//
// 覆盖规则：覆盖 (id, 成员, [s,e)) 左闭右开，可相互重叠及与轮值重叠；
// 重叠处最后添加且未被删除的覆盖生效。删除后其时段恢复为下层生效者；
// 已删除的 id 可再次添加并视为全新的最后添加者。
//
// 时间线合并：仅当相邻两段成员与来源都相同才合并；来源为 "rotation"
// 或生效覆盖的 id。
//
// 所有导出方法均可并发调用，效果等价于某个串行顺序。
package schedule

import (
	"errors"
	"sort"
	"sync"
)

// RotationSource 是当值来源为轮值（而非任何覆盖）时的来源标识。
const RotationSource = "rotation"

// MaxSegments 是 Timeline 单次返回的最大段数，超过则整体拒绝。
const MaxSegments = 10000

// 各类拒绝原因，调用方可用 errors.Is 区分。
var (
	ErrEmptyRoster        = errors.New("schedule: roster is empty")
	ErrDuplicateMember    = errors.New("schedule: roster has duplicate member")
	ErrNonPositivePeriod  = errors.New("schedule: period L must be positive")
	ErrInvalidInterval    = errors.New("schedule: interval is empty or reversed")
	ErrMemberNotInRoster  = errors.New("schedule: override member not in roster")
	ErrDuplicateOverlayID = errors.New("schedule: override id already exists")
	ErrOverrideNotFound   = errors.New("schedule: override id not found")
	ErrTooManySegments    = errors.New("schedule: timeline exceeds 10000 segments")
)

// Segment 是 Timeline 返回的最大连续段 [Start, End)。
type Segment struct {
	Start  int64
	End    int64
	Member string
	Source string // RotationSource 或生效覆盖的 id
}

type override struct {
	id     string
	member string
	s, e   int64
	seq    int64 // 单调递增的添加序号，决定重叠时的优先级
}

// Scheduler 是并发安全的值班轮值表。
type Scheduler struct {
	mu       sync.RWMutex
	members  []string
	inRoster map[string]bool
	t0       int64
	period   int64
	active   map[string]*override
	seq      int64
}

// NewScheduler 校验名册与班长并创建轮值表。
func NewScheduler(members []string, t0, period int64) (*Scheduler, error) {
	if len(members) == 0 {
		return nil, ErrEmptyRoster
	}
	seen := make(map[string]bool, len(members))
	for _, m := range members {
		if seen[m] {
			return nil, ErrDuplicateMember
		}
		seen[m] = true
	}
	if period <= 0 {
		return nil, ErrNonPositivePeriod
	}
	cp := make([]string, len(members))
	copy(cp, members)
	return &Scheduler{
		members:  cp,
		inRoster: seen,
		t0:       t0,
		period:   period,
		active:   make(map[string]*override),
	}, nil
}

// AddOverride 添加覆盖 [s, e)。校验顺序：区间、成员是否在名册、id 是否重复。
// 任何校验失败都不改变覆盖集合。
func (s *Scheduler) AddOverride(id, member string, start, end int64) error {
	if start >= end {
		return ErrInvalidInterval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.inRoster[member] {
		return ErrMemberNotInRoster
	}
	if _, ok := s.active[id]; ok {
		return ErrDuplicateOverlayID
	}
	s.seq++
	s.active[id] = &override{id: id, member: member, s: start, e: end, seq: s.seq}
	return nil
}

// RemoveOverride 删除覆盖；id 不存在（含已删除）时拒绝。
func (s *Scheduler) RemoveOverride(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.active[id]; !ok {
		return ErrOverrideNotFound
	}
	delete(s.active, id)
	return nil
}

// Who 返回时刻 t 的当值成员与来源。
func (s *Scheduler) Who(t int64) (member, source string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ov := s.topOverrideAtLocked(t); ov != nil {
		return ov.member, ov.id
	}
	return s.rotationMember(t), RotationSource
}

// Timeline 返回 [a, b) 内合并后的最大连续段。
func (s *Scheduler) Timeline(a, b int64) ([]Segment, error) {
	if a >= b {
		return nil, ErrInvalidInterval
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 事件点：区间端点 + 所有活动覆盖（裁剪到 [a,b)）的端点。
	points := []int64{a, b}
	for _, ov := range s.active {
		lo := maxInt64(ov.s, a)
		hi := minInt64(ov.e, b)
		if lo < hi {
			points = append(points, lo, hi)
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	uniq := points[:1]
	for _, p := range points[1:] {
		if p != uniq[len(uniq)-1] {
			uniq = append(uniq, p)
		}
	}

	var raw []Segment
	rotationSegs := 0
	emitRotation := func(p, q int64) error {
		// 轮值段不会被任何相邻段合并（来源不同或成员不同），
		// 因此其数量是最终段数的下界，可用于提前拒绝。
		start := p
		for k := floorDiv(p-s.t0, s.period) + 1; ; k++ {
			boundary := s.t0 + k*s.period
			if boundary >= q {
				break
			}
			raw = append(raw, Segment{start, boundary, s.rotationMember(start), RotationSource})
			rotationSegs++
			if rotationSegs > MaxSegments {
				return ErrTooManySegments
			}
			start = boundary
		}
		raw = append(raw, Segment{start, q, s.rotationMember(start), RotationSource})
		rotationSegs++
		if rotationSegs > MaxSegments {
			return ErrTooManySegments
		}
		return nil
	}

	for i := 0; i+1 < len(uniq); i++ {
		p, q := uniq[i], uniq[i+1]
		if ov := s.topOverrideAtLocked(p); ov != nil {
			raw = append(raw, Segment{p, q, ov.member, ov.id})
		} else if err := emitRotation(p, q); err != nil {
			return nil, err
		}
	}

	merged := make([]Segment, 0, len(raw))
	for _, seg := range raw {
		if n := len(merged); n > 0 && merged[n-1].Member == seg.Member && merged[n-1].Source == seg.Source {
			merged[n-1].End = seg.End
			continue
		}
		merged = append(merged, seg)
	}
	if len(merged) > MaxSegments {
		return nil, ErrTooManySegments
	}
	return merged, nil
}

// topOverrideAtLocked 返回覆盖 t 的最后添加的活动覆盖，无则 nil。
func (s *Scheduler) topOverrideAtLocked(t int64) *override {
	var top *override
	for _, ov := range s.active {
		if ov.s <= t && t < ov.e && (top == nil || ov.seq > top.seq) {
			top = ov
		}
	}
	return top
}

// rotationMember 返回时刻 t 按名册循环的轮值成员。
func (s *Scheduler) rotationMember(t int64) string {
	idx := floorMod(floorDiv(t-s.t0, s.period), int64(len(s.members)))
	return s.members[idx]
}

func floorDiv(a, b int64) int64 { // b > 0
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func floorMod(a, b int64) int64 { // b > 0，结果非负
	r := a % b
	if r < 0 {
		r += b
	}
	return r
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
