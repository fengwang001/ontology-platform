package ontology

import (
	"math"
	"math/big"
	"math/rand"
	"sort"
	"sync"
)

// Engine 是电表换表与读数衔接引擎。所有方法并发安全：
// 读操作取读锁，写操作取写锁，整体效果等价于某种串行执行顺序。
type Engine struct {
	mu     sync.RWMutex
	meters map[string]*meterState
	points map[string]*pointState
	rng    *rand.Rand
}

func NewEngine() *Engine {
	return &Engine{
		meters: make(map[string]*meterState),
		points: make(map[string]*pointState),
		rng:    rand.New(rand.NewSource(1)),
	}
}

// CreateMeter 登记一只电表的静态属性。
func (e *Engine) CreateMeter(m Meter) error {
	if m.ID == "" {
		return errf(ErrInvalidArgument, "电表标识为空")
	}
	if m.Digits < 1 || m.Digits > 18 {
		return errf(ErrInvalidArgument, "显示位数 %d 超出 1..18", m.Digits)
	}
	if m.Multiplier <= 0 {
		return errf(ErrInvalidArgument, "倍率必须为正数，得到 %d", m.Multiplier)
	}
	if m.MaxUsagePerUnitTime < 0 {
		return errf(ErrInvalidArgument, "单位时间上限不可为负")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.meters[m.ID]; ok {
		return errf(ErrInvalidArgument, "电表 %s 已存在", m.ID)
	}
	def := m
	e.meters[m.ID] = &meterState{
		def: &def,
		mod: modulus(m.Digits),
		// installTime=1、removeTime=0 表示“从未挂接”：任何非负时刻都不在挂接期内。
		installTime: 1,
		removeTime:  0,
		tree:        newReadingTree(modulus(m.Digits), e.rng),
	}
	return nil
}

func validTime(t int64) bool { return t >= 0 && t < math.MaxInt64 }

// Attach 在供电点上首次（或拆除后重新）安装一只电表，同时写入安装时刻的实抄初始读数。
func (e *Engine) Attach(pointID, meterID string, at int64, initial uint64) error {
	if pointID == "" {
		return errf(ErrInvalidArgument, "供电点标识为空")
	}
	if meterID == "" {
		return errf(ErrInvalidArgument, "电表标识为空")
	}
	if !validTime(at) {
		return errf(ErrInvalidArgument, "安装时刻越界")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.meters[meterID]
	if !ok {
		return errf(ErrInvalidArgument, "电表 %s 不存在", meterID)
	}
	if initial >= m.mod {
		return errf(ErrInvalidArgument, "显示值 %d 超出 %d 位显示范围", initial, m.def.Digits)
	}
	p := e.points[pointID]
	if p == nil {
		p = &pointState{id: pointID}
		e.points[pointID] = p
	}
	if len(p.segments) > 0 && p.segments[len(p.segments)-1].removeTime == math.MaxInt64 {
		return errf(ErrReadingScheduleConflict, "供电点 %s 已挂接电表", pointID)
	}
	if m.pointID != "" {
		return errf(ErrReadingScheduleConflict, "电表 %s 已挂在供电点 %s", meterID, m.pointID)
	}
	if last, ok := m.tree.last(); ok && last.Time > at {
		return errf(ErrReadingScheduleConflict, "安装时刻 %d 早于电表已有读数时刻 %d", at, last.Time)
	}
	if ex, ok := m.tree.get(at); ok {
		if ex.Kind != Actual || ex.Value != initial {
			return errf(ErrReadingConflict, "时刻 %d 已存在异值/估算读数", at)
		}
		return nil
	}
	m.tree.insert(Reading{Time: at, Value: initial, Kind: Actual})
	m.pointID = pointID
	m.installTime = at
	m.removeTime = math.MaxInt64 // 重新挂接：清除上次拆除留下的端点
	p.segments = append(p.segments, segment{meterID: meterID, installTime: at, removeTime: math.MaxInt64})
	return nil
}

// ReplaceMeter 在 at 时刻完成旧表拆除与新表安装。
// 旧表写入末次实抄读数，新表写入初始实抄读数；两条读数均为换表衔接点。
func (e *Engine) ReplaceMeter(pointID string, at int64, oldLast uint64, newMeterID string, newInitial uint64) error {
	if pointID == "" {
		return errf(ErrInvalidArgument, "供电点标识为空")
	}
	if newMeterID == "" {
		return errf(ErrInvalidArgument, "电表标识为空")
	}
	if !validTime(at) {
		return errf(ErrInvalidArgument, "换表时刻越界")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.points[pointID]
	if p == nil || len(p.segments) == 0 {
		return errf(ErrReadingScheduleConflict, "供电点 %s 无挂接记录", pointID)
	}
	cur := p.segments[len(p.segments)-1]
	if cur.removeTime != math.MaxInt64 {
		return errf(ErrReadingScheduleConflict, "供电点 %s 当前无挂接中的电表", pointID)
	}
	old := e.meters[cur.meterID]
	if old == nil {
		return errf(ErrInvalidArgument, "旧电表 %s 不存在", cur.meterID)
	}
	nw := e.meters[newMeterID]
	if nw == nil {
		return errf(ErrInvalidArgument, "新电表 %s 不存在", newMeterID)
	}
	if newMeterID == cur.meterID {
		return errf(ErrInvalidArgument, "新表不得与旧表为同一只电表")
	}
	if oldLast >= old.mod {
		return errf(ErrInvalidArgument, "旧表末次读数 %d 超出 %d 位显示范围", oldLast, old.def.Digits)
	}
	if newInitial >= nw.mod {
		return errf(ErrInvalidArgument, "新表初始读数 %d 超出 %d 位显示范围", newInitial, nw.def.Digits)
	}
	// 时刻次序：换表时刻不得早于旧表最新读数，也不得早于新表任何已有记录。
	if last, ok := old.tree.last(); ok && at < last.Time {
		return errf(ErrReadingScheduleConflict, "换表时刻 %d 早于旧表最新读数时刻 %d", at, last.Time)
	}
	if last, ok := nw.tree.last(); ok && at < last.Time {
		return errf(ErrReadingScheduleConflict, "换表时刻 %d 早于新表已有读数时刻 %d", at, last.Time)
	}
	if nw.pointID != "" {
		return errf(ErrReadingScheduleConflict, "新表 %s 已挂在供电点 %s", newMeterID, nw.pointID)
	}
	// 同刻读数一致性（读数冲突）。
	oldExisting, oldHas := old.tree.get(at)
	if oldHas {
		switch {
		case oldExisting.Kind == Actual && oldExisting.Value != oldLast:
			return errf(ErrReadingConflict, "旧表时刻 %d 已存在异值实抄读数", at)
		case oldExisting.Kind == Estimated:
			if err := checkReplacementRationality(old, at, oldLast); err != nil {
				return err
			}
		}
	}
	newExisting, newHas := nw.tree.get(at)
	if newHas && (newExisting.Kind != Actual || newExisting.Value != newInitial) {
		return errf(ErrReadingConflict, "新表时刻 %d 已存在异值/估算读数", at)
	}
	// 合理性：旧表新增末次读数的前驱区间。
	if !oldHas || oldExisting.Kind == Estimated {
		if pred, hasPred := old.tree.predInTour(at, old.installTime, old.removeTime); hasPred {
			if err := checkEdge(old, pred, Reading{Time: at, Value: oldLast, Kind: Actual}); err != nil {
				return err
			}
		}
	}
	// —— 全部校验通过，提交变更 ——
	if oldHas && oldExisting.Kind == Estimated {
		old.tree.delete(at)
	}
	if !oldHas || oldExisting.Kind == Estimated {
		old.tree.insert(Reading{Time: at, Value: oldLast, Kind: Actual})
	}
	if !newHas {
		nw.tree.insert(Reading{Time: at, Value: newInitial, Kind: Actual})
	}
	old.removeTime = at
	old.pointID = ""
	nw.pointID = pointID
	nw.installTime = at
	nw.removeTime = math.MaxInt64
	p.segments[len(p.segments)-1].removeTime = at
	p.segments = append(p.segments, segment{meterID: newMeterID, installTime: at, removeTime: math.MaxInt64})
	return nil
}

// checkEdge 校验两条相邻读数构成的单个区间：翻转判定后的合理性上限（取等通过）。
func checkEdge(m *meterState, prev, cur Reading) error {
	delta, _ := rawDelta(prev.Value, cur.Value, m.mod)
	duration := cur.Time - prev.Time
	if !withinRationalLimit(delta, m.def.Multiplier, duration, m.def.MaxUsagePerUnitTime) {
		return errf(ErrUnreasonable,
			"区间 [%d,%d] 表显用电 %d（倍率 %d）超出单位时间上限 %d",
			prev.Time, cur.Time, delta, m.def.Multiplier, m.def.MaxUsagePerUnitTime)
	}
	return nil
}

// checkReplacementRationality 校验某时刻估算读数被实抄值替换后，两个相邻区间的合理性。
func checkReplacementRationality(m *meterState, at int64, newValue uint64) error {
	cur := Reading{Time: at, Value: newValue, Kind: Actual}
	if pred, ok := m.tree.predInTour(at, m.installTime, m.removeTime); ok {
		if err := checkEdge(m, pred, cur); err != nil {
			return err
		}
	}
	if succ, ok := m.tree.succInTour(at, m.installTime, m.removeTime); ok {
		if err := checkEdge(m, cur, succ); err != nil {
			return err
		}
	}
	return nil
}

// RegisterReading 对某电表登记一条读数。
func (e *Engine) RegisterReading(meterID string, at int64, value uint64, kind ReadingKind) error {
	if meterID == "" {
		return errf(ErrInvalidArgument, "电表标识为空")
	}
	if !validTime(at) {
		return errf(ErrInvalidArgument, "读数时刻越界")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.meters[meterID]
	if m == nil {
		return errf(ErrInvalidArgument, "电表 %s 不存在", meterID)
	}
	if value >= m.mod {
		return errf(ErrInvalidArgument, "显示值 %d 超出 %d 位显示范围", value, m.def.Digits)
	}
	if at < m.installTime || at >= m.removeTime {
		return errf(ErrNotAttached, "时刻 %d 不在电表 %s 的当前挂接期内", at, meterID)
	}
	if ex, ok := m.tree.get(at); ok {
		switch {
		case ex.Kind == Actual && kind == Actual && ex.Value == value:
			return nil
		case ex.Kind == Actual:
			return errf(ErrReadingConflict, "时刻 %d 已存在实抄读数 %d", at, ex.Value)
		case kind == Estimated:
			return errf(ErrReadingConflict, "时刻 %d 已存在估算读数", at)
		default:
			if err := checkReplacementRationality(m, at, value); err != nil {
				return err
			}
			m.tree.delete(at)
			m.tree.insert(Reading{Time: at, Value: value, Kind: Actual})
			return nil
		}
	}
	pred, hasPred := m.tree.predInTour(at, m.installTime, m.removeTime)
	succ, hasSucc := m.tree.succInTour(at, m.installTime, m.removeTime)
	cur := Reading{Time: at, Value: value, Kind: kind}
	if kind == Estimated {
		if hasSucc {
			return errf(ErrReadingScheduleConflict, "估算读数时刻 %d 早于已有最新读数 %d", at, succ.Time)
		}
	} else if hasSucc {
		_, oldRO := rawDelta(pred.Value, succ.Value, m.mod)
		d1, r1 := rawDelta(pred.Value, value, m.mod)
		d2, r2 := rawDelta(value, succ.Value, m.mod)
		if boolCount(r1)+boolCount(r2) != boolCount(oldRO) {
			return errf(ErrReadingScheduleConflict,
				"在 %d 插入读数破坏翻转守恒：原区间翻转 %t，拆分后翻转 %t/%t（拆分用电量 %d/%d）",
				at, oldRO, r1, r2, d1, d2)
		}
	}
	if hasPred {
		if err := checkEdge(m, pred, cur); err != nil {
			return err
		}
	}
	if hasSucc {
		if err := checkEdge(m, cur, succ); err != nil {
			return err
		}
	}
	m.tree.insert(cur)
	return nil
}

func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DeleteEstimatedReading 删除一条估算读数；实抄读数不可删除。
// 删除后前后区间合并为一条区间并重新判定；不通过则删除被拒绝。
func (e *Engine) DeleteEstimatedReading(meterID string, at int64) error {
	if meterID == "" {
		return errf(ErrInvalidArgument, "电表标识为空")
	}
	if !validTime(at) {
		return errf(ErrInvalidArgument, "读数时刻越界")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.meters[meterID]
	if m == nil {
		return errf(ErrInvalidArgument, "电表 %s 不存在", meterID)
	}
	if at < m.installTime || at >= m.removeTime {
		return errf(ErrNotAttached, "时刻 %d 不在电表 %s 的当前挂接期内", at, meterID)
	}
	ex, ok := m.tree.get(at)
	if !ok {
		return errf(ErrNoReading, "电表 %s 在时刻 %d 无读数", meterID, at)
	}
	if ex.Kind == Actual {
		return errf(ErrReadingConflict, "时刻 %d 为实抄读数，不可删除", at)
	}
	// 合并区间重新判定。
	pred, hasPred := m.tree.predInTour(at, m.installTime, m.removeTime)
	succ, hasSucc := m.tree.succInTour(at, m.installTime, m.removeTime)
	if hasPred && hasSucc {
		if err := checkEdge(m, pred, succ); err != nil {
			return err
		}
	}
	m.tree.delete(at)
	return nil
}

// QueryUsage 查询供电点 [from,to] 内的用电量。
// from、to 必须都是该供电点某只电表上的已有读数时刻（含换表衔接点），
// 否则报「无读数」。换表时刻两只表的读数不产生用电量（区间在各表树内各自闭合）。
func (e *Engine) QueryUsage(pointID string, from, to int64) (QueryResult, error) {
	if pointID == "" {
		return QueryResult{}, errf(ErrInvalidArgument, "供电点标识为空")
	}
	if !validTime(from) || !validTime(to) {
		return QueryResult{}, errf(ErrInvalidArgument, "查询时刻越界")
	}
	if from > to {
		return QueryResult{}, errf(ErrInvalidArgument, "查询起始时刻 %d 晚于结束时刻 %d", from, to)
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	p := e.points[pointID]
	if p == nil {
		return QueryResult{}, errf(ErrNoReading, "供电点 %s 不存在", pointID)
	}
	// 先确认两个查询端点都是某只挂接表上的已有读数时刻。
	loSeg, loOK := segmentForReading(p, from)
	hiSeg, hiOK := segmentForReading(p, to)
	if !loOK || !hiOK {
		return QueryResult{}, errf(ErrNoReading, "时刻 %d 或 %d 不是供电点 %s 上的已有读数时刻", from, to, pointID)
	}
	lo := loSeg
	hi := hiSeg
	if lo > hi {
		lo, hi = hi, lo
	}
	total := big.NewInt(0)
	containsEst := false
	for i := lo; i <= hi; i++ {
		seg := p.segments[i]
		m := e.meters[seg.meterID]
		// 每段贡献其读数树在查询时间序 (from,to] 内的区间。
		// 中间段整段计入（衔接时刻是旧段末读数、也是新段首读数，各在各树内
		// 自然闭合，不会跨树产生区间）；左端段下界取 from，右端段上界取 to。
		l := from
		if seg.installTime > l {
			l = seg.installTime
		}
		h := to
		if seg.removeTime <= h {
			h = seg.removeTime
		}
		if l >= h {
			continue
		}
		if _, ok := m.tree.get(l); !ok {
			return QueryResult{}, errf(ErrNoReading, "时刻 %d 在电表 %s 上无读数", l, seg.meterID)
		}
		if _, ok := m.tree.get(h); !ok {
			return QueryResult{}, errf(ErrNoReading, "时刻 %d 在电表 %s 上无读数", h, seg.meterID)
		}
		sumRaw, estEdges := m.tree.rangeAgg(l, h)
		total.Add(total, new(big.Int).Mul(sumRaw, big.NewInt(m.def.Multiplier)))
		if estEdges > 0 {
			containsEst = true
		}
	}
	return QueryResult{Usage: total, ContainsEstimated: containsEst}, nil
}

// segmentForReading 二分定位某读数时刻归属的挂接段下标。
// 段按安装时刻有序、首尾相接；换表衔接时刻 t 同时是旧段右端点与新段左端点，
// 约定归属旧段（最后一个 installTime <= t 的段），保证 from/to 为衔接时刻时
// 两侧段都被纳入查询，衔接段自身长度为零、不产生用电量。
func segmentForReading(p *pointState, t int64) (int, bool) {
	segs := p.segments
	idx := sort.Search(len(segs), func(i int) bool { return segs[i].installTime > t }) - 1
	if idx < 0 || segs[idx].removeTime < t {
		return 0, false
	}
	return idx, true
}
