package shophours

// seg 是一段不含回绕的线性区间 [start,end)，owned 指出其来源表区间的下标，
// 用于回绕区间首尾相接时不误判为“两个不同区间相接”。
type seg struct {
	start int64
	end   int64
	owned int
}

// schedule 是一份营业时段表的内部表示：以周内每秒为下标的数组，
// cover[t] 为该秒所属（或临近）营业区间的右端点（绝对周秒偏移），不营业时为 -1。
type schedule struct {
	weekSec int64
	cover   []int64
}

func validateIntervals(ivs []Interval, weekSec int64) error {
	if weekSec <= 0 {
		return bizErr(ErrInvalidParam, "week seconds must be positive")
	}
	segs := make([]seg, 0, len(ivs)*2)
	for i, iv := range ivs {
		if iv.Start < 0 || iv.Start > weekSec || iv.End < 0 || iv.End > weekSec || iv.Start == iv.End {
			return bizErr(ErrInvalidParam, "interval endpoints out of range or degenerate")
		}
		if iv.Start < iv.End {
			segs = append(segs, seg{iv.Start, iv.End, i})
		} else {
			segs = append(segs, seg{0, iv.End, i}, seg{iv.Start, weekSec, i})
		}
	}
	// 按起点排序；起点相同即重叠。简单插入/标准排序均可，表规模通常很小。
	for i := 1; i < len(segs); i++ {
		for j := i; j > 0 && segs[j-1].start > segs[j].start; j-- {
			segs[j-1], segs[j] = segs[j], segs[j-1]
		}
	}
	for i := 1; i < len(segs); i++ {
		prev, cur := segs[i-1], segs[i]
		if cur.start < prev.end || (cur.start == prev.end && cur.owned != prev.owned) {
			return bizErr(ErrInvalidParam, "intervals overlap or touch")
		}
	}
	// 回绕检查：排序后首段（自 0 开始）与末段（以 weekSec 结束）若来自不同原始区间，
	// 则它们在周回绕处相接或重叠。
	if n := len(segs); n >= 2 {
		first, last := segs[0], segs[n-1]
		if first.start == 0 && last.end == weekSec && first.owned != last.owned {
			return bizErr(ErrInvalidParam, "intervals overlap or touch across week wrap")
		}
	}
	return nil
}

func buildSchedule(ivs []Interval, weekSec int64) *schedule {
	sc := &schedule{weekSec: weekSec, cover: make([]int64, weekSec)}
	for i := range sc.cover {
		sc.cover[i] = -1
	}
	for _, iv := range ivs {
		if iv.Start < iv.End {
			for t := iv.Start; t < iv.End; t++ {
				sc.cover[t] = iv.End
			}
		} else {
			for t := int64(0); t < iv.End; t++ {
				sc.cover[t] = iv.End
			}
			for t := iv.Start; t < weekSec; t++ {
				sc.cover[t] = weekSec + iv.End
			}
		}
	}
	return sc
}

// containingEnd 判定绝对时刻 t 是否落在营业区间内；若是，返回该区间的绝对右端点。
// ok=false 表示不营业。
func (s *schedule) containingEnd(t, baseTime, weekSec int64) (int64, bool) {
	off := mod(t-baseTime, weekSec)
	relEnd := s.cover[off]
	if relEnd < 0 {
		return 0, false
	}
	return t + (relEnd - off), true
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func mod(a, b int64) int64 { return a - floorDiv(a, b)*b }

// scheduleBook 管理当前生效表与至多一份待生效表。
type scheduleBook struct {
	baseTime int64
	weekSec  int64
	current  *schedule
	pending  *schedule
	fromWeek int64
}

func newScheduleBook(baseTime, weekSec int64, ivs []Interval) *scheduleBook {
	return &scheduleBook{
		baseTime: baseTime,
		weekSec:  weekSec,
		current:  buildSchedule(ivs, weekSec),
		pending:  nil,
		fromWeek: 0,
	}
}

// advance 将不晚于 now 所属周边界的待生效表转正。
func (b *scheduleBook) advance(now int64) {
	if b.pending == nil {
		return
	}
	weekStart := b.baseTime + floorDiv(now-b.baseTime, b.weekSec)*b.weekSec
	if weekStart >= b.fromWeek {
		b.current = b.pending
		b.pending = nil
	}
}

// submit 登记一份将在下一个周边界生效的新表，覆盖既有待生效表。
func (b *scheduleBook) submit(now int64, ivs []Interval) error {
	if err := validateIntervals(ivs, b.weekSec); err != nil {
		return err
	}
	b.advance(now)
	nextBoundary := b.baseTime + (floorDiv(now-b.baseTime, b.weekSec)+1)*b.weekSec
	b.pending = buildSchedule(ivs, b.weekSec)
	b.fromWeek = nextBoundary
	return nil
}

// forWeek 返回 weekStart 所在周使用的表（待生效表届时已生效则用它）。
func (b *scheduleBook) forWeek(weekStart int64) *schedule {
	if b.pending != nil && weekStart >= b.fromWeek {
		return b.pending
	}
	return b.current
}
