package temporalauth

// Instant 是归一化后的绝对时刻：自 Unix 纪元起的 UTC 秒数。
// 系统内部所有判定均在 Instant（同一基准）上进行。
type Instant int64

// Civil 是带日历日期的民用墙钟时间（不含任何时区语义）。
type Civil struct {
	Year   int
	Month  int // 1..12
	Day    int // 1..31
	Hour   int // 0..23
	Minute int // 0..59
	Second int // 0..59
}

// Transition 描述某条时区规则中一次偏移量跃迁（标准时/夏令时切换点）。
// At 为跃迁发生的 UTC 时刻；OffsetAfter 为跃迁后生效的 UTC 偏移秒数。
type Transition struct {
	At          Instant
	OffsetAfter int64
}

// ZoneRules 是不依赖 IANA 数据库的确定性时区规则：一个基础偏移与一条
// 严格按 At 升序排列的偏移跃迁表。相同输入在任何机器、任何时刻得到相同结果。
type ZoneRules struct {
	ID          string
	BaseOffset  int64
	Transitions []Transition
}

// ZoneCatalog 是不可变时区规则目录，按 ID 索引。
type ZoneCatalog struct {
	zones map[string]*ZoneRules
}

// NewZoneCatalog 构造不可变时区目录。
func NewZoneCatalog(rules ...*ZoneRules) *ZoneCatalog {
	c := &ZoneCatalog{zones: map[string]*ZoneRules{}}
	for _, r := range rules {
		c.zones[r.ID] = r
	}
	return c
}

func (c *ZoneCatalog) Get(id string) (*ZoneRules, bool) {
	r, ok := c.zones[id]
	return r, ok
}

// OffsetAt 返回绝对时刻 t 生效的 UTC 偏移秒数。跃迁点 At 本身起采用
// OffsetAfter（切换瞬间归入新偏移），全程为一致的半开约定。
func (r *ZoneRules) OffsetAt(t Instant) int64 {
	offset := r.BaseOffset
	for _, tr := range r.Transitions {
		if t >= tr.At {
			offset = tr.OffsetAfter
		} else {
			break
		}
	}
	return offset
}

// offsetBefore 返回紧邻第 trIndex 个跃迁点之前生效的偏移。
func (r *ZoneRules) offsetBefore(trIndex int) int64 {
	if trIndex == 0 {
		return r.BaseOffset
	}
	return r.Transitions[trIndex-1].OffsetAfter
}

// transitionIndexAfter 返回首个 At > t 的跃迁下标（t 之后的下一次切换）。
func (r *ZoneRules) transitionIndexAfter(t Instant) int {
	lo, hi := 0, len(r.Transitions)
	for lo < hi {
		mid := (lo + hi) / 2
		if r.Transitions[mid].At <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

var daysBeforeMonth = [12]int64{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}

func civilToSeconds(c Civil) int64 {
	y := int64(c.Year) - 1
	days := 365*y + y/4 - y/100 + y/400
	days += daysBeforeMonth[c.Month-1]
	if c.Month > 2 && isLeap(c.Year) {
		days++
	}
	days += int64(c.Day) - 1
	return (days-719162)*86400 + int64(c.Hour)*3600 + int64(c.Minute)*60 + int64(c.Second)
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// secondsToCivil 使用以 3 月为岁首的 proleptic Gregorian 换算（Howard Hinnant
// civil-from-days 算法），正确处理闰年与负天数（1970 年之前）。
func secondsToCivil(s int64) Civil {
	days := floorDiv(s, 86400)
	tod := s - days*86400

	z := days + 719468
	era := floorDiv(z, 146097)
	if z < 0 {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	day := doy - (153*mp+2)/5 + 1
	month := mp + 3
	if month > 12 {
		month -= 12
	}
	if month <= 2 {
		y++
	}

	return Civil{
		Year:   int(y),
		Month:  int(month),
		Day:    int(day),
		Hour:   int(tod / 3600),
		Minute: int((tod % 3600) / 60),
		Second: int(tod % 60),
	}
}

// InstantToCivil 把绝对时刻按规则 r 换算为本地民用墙钟时间。
func (r *ZoneRules) InstantToCivil(t Instant) Civil {
	return secondsToCivil(int64(t) + r.OffsetAt(t))
}

// CivilToInstant 把墙钟时间解析为绝对时刻。
//
// gap（弹簧向前跳过的本地时间）：该本地时间在本时区不存在，归一化到
// 触发该 gap 的跃迁点 At（唯一结果）。
// overlap（秋季回退的重复本地时间）：同一墙钟对应两个瞬间，取偏移更大的
// 更早瞬间。
// 上述约定仅由偏移量与跃迁表机械决定，在夏令时切换当天与普通日期完全一致，
// 不设任何特例分支。
func (r *ZoneRules) CivilToInstant(c Civil) Instant {
	wall := civilToSeconds(c)

	type candidate struct {
		t       Instant
		idx     int // t 之后首个跃迁下标
		offset  int64
		illegal bool
	}

	// 收集所有不同偏移（基础偏移 + 每次跃迁后的偏移），构造候选瞬间。
	offsetSet := map[int64]struct{}{r.BaseOffset: {}}
	offsets := []int64{r.BaseOffset}
	for _, tr := range r.Transitions {
		if _, seen := offsetSet[tr.OffsetAfter]; !seen {
			offsetSet[tr.OffsetAfter] = struct{}{}
			offsets = append(offsets, tr.OffsetAfter)
		}
	}

	candidates := make([]candidate, 0, len(offsets))
	for _, off := range offsets {
		t := Instant(wall - off)
		candidates = append(candidates, candidate{
			t:       t,
			idx:     r.transitionIndexAfter(t),
			offset:  off,
			illegal: r.InstantToCivil(t) != c,
		})
	}

	// 普通时刻或 overlap：取合法候选中最早的瞬间（即偏移更大者）。
	var best Instant
	legalCount := 0
	for _, cand := range candidates {
		if !cand.illegal && (legalCount == 0 || cand.t < best) {
			best = cand.t
			legalCount++
		}
	}
	if legalCount > 0 {
		return best
	}

	// gap：全部候选均不合法。墙钟被某次向前跃迁跳过。对任一候选瞬间 t，
	// 其后第一次偏移增加（delta>0）的跃迁点即 gap 起点；当“用跃迁前偏移
	// 解释的墙钟”严格早于 t 时，t 正处于被跳过区间内，归一化到跃迁点 At。
	// 多个候选给出同一跃迁点，结果唯一。
	for _, cand := range candidates {
		for i := cand.idx; i < len(r.Transitions); i++ {
			tr := r.Transitions[i]
			delta := tr.OffsetAfter - r.offsetBefore(i)
			if delta <= 0 {
				continue
			}
			wallBefore := int64(tr.At) + r.offsetBefore(i)
			if wallBefore < wall {
				return tr.At
			}
			break
		}
	}
	return candidates[0].t
}
