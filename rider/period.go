package rider

// riderState 为单个骑手的全部状态。
type riderState struct {
	id         string
	clusters   *clusterIndex
	periods    map[int]*periodState
	nextSettle int         // 下一个待结算周期（从 0 起严格递增）
	lastRights int         // 上一个已结算周期的实际权益等级
	overrides  map[int]int // 回溯改写：周期 -> 下降上限基准等级
}

// periodState 为单个骑手单个周期的账目。
type periodState struct {
	liveScore     int           // 当前未撤销且计扣分事件扣分之和（恒等不变量）
	counting      map[int64]int // 当前计扣分事件 ID -> 分值
	settled       bool
	frozenScore   int           // 结算时刻锁定的扣分总额
	frozenContrib map[int64]int // 结算时刻计扣分事件 ID -> 分值（撤销时据此算本应分）
	level         int           // 结算等级
	rights        int           // 实际权益等级
	baseline      int           // 结算时实际使用的下降上限基准（回溯重算用）
}

func newRiderState(id string) *riderState {
	return &riderState{
		id:        id,
		clusters:  newClusterIndex(),
		periods:   map[int]*periodState{},
		overrides: map[int]int{},
	}
}

func (rs *riderState) period(p int) *periodState {
	ps, ok := rs.periods[p]
	if !ok {
		ps = &periodState{counting: map[int64]int{}}
		rs.periods[p] = ps
	}
	return ps
}

// settle 把右端点不晚于 now 的周期按顺序全部结算。
// 结算纯由时刻驱动：任何以不早于周期右端点的时刻执行的操作/查询都会触发。
func (s *System) settle(rs *riderState, now int64) {
	for {
		p := rs.nextSettle
		if s.periodRight(p) > now {
			return
		}
		ps := rs.period(p)
		ps.settled = true
		ps.frozenScore = ps.liveScore
		ps.frozenContrib = make(map[int64]int, len(ps.counting))
		for id, score := range ps.counting {
			ps.frozenContrib[id] = score
		}
		ps.level = s.levelOf(ps.frozenScore)
		baseline := rs.lastRights
		if ob, ok := rs.overrides[p]; ok {
			baseline = ob
			delete(rs.overrides, p)
		}
		ps.baseline = baseline
		capped := baseline + s.cfg.MaxLevelDrop
		if ps.level < capped {
			capped = ps.level
		}
		ps.rights = capped
		rs.lastRights = capped
		rs.nextSettle = p + 1
	}
}

// levelOf 按阈值定级：扣分恰等于阈值落入更差一级。
func (s *System) levelOf(score int) int {
	level := 0
	for _, th := range s.cfg.Thresholds {
		if score >= th {
			level++
		} else {
			break
		}
	}
	return level
}

// periodOf 返回时刻 t 所属周期（左闭右开；恰在右端点归入下一周期）。
// 对负时刻做地板除法修正。
func (s *System) periodOf(t int64) int {
	L := s.cfg.PeriodLength
	x := t - s.cfg.PeriodOrigin
	q := x / L
	if x%L != 0 && x < 0 {
		q--
	}
	return int(q)
}

func (s *System) periodRight(period int) int64 {
	return s.cfg.PeriodOrigin + int64(period+1)*s.cfg.PeriodLength
}
